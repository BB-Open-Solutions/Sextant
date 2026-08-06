# Threat model

Roadmap E. Grounded in the code as of the 0.65.x line; every control
below is cited to `file:line` so this document can be re-verified, not
trusted. It records what the design defends, how, and - equally - the
residual risks it does not yet close. Two independent security reviews
(code + security) vetted the M3 surface; this consolidates their result
into a standing model.

## Scope and assumptions

Sextant is a fleet control plane: a console + API that edit one
organisation's configuration as data (`fleet.json`) in a git repo, a
nix gate that validates every write, a device agent that pulls its
configuration through a git funnel, and a root action executor for
destructive remote actions. One tenant per pod/cell (ADR 0009).

Assumed trustworthy: the Kubernetes cluster and its secret store, the
git remote's own access control, the OIDC issuer (ADR 0015), and the
build/CI environment. Assumed hostile: the public network, an
authenticated operator acting beyond their scope, a compromised or
lost device, and malicious configuration values submitted through the
console/API.

## Actors and trust boundaries

- **Anonymous network.** Reaches only login, token auth and check-in,
  all rate-limited (`internal/http/mw/ratelimit.go:37`).
- **Operator** (session or personal token). Authority derived per
  request from the live document, never stored
  (`internal/http/api/authz.go:93-105`).
- **Machine principal** (device/station credential, or the CI/rollout
  identity). Bound to one subject; cannot reach operator endpoints
  (`internal/app/token.go:124-127`).
- **Break-glass static token.** Full authority, constant-time compared
  (`authz.go:56-57`); an operational last resort.

The primary boundary the whole design turns on: **the console writes
data, the gate turns data into a validated system, and only then does
git record it.** Everything downstream (funnel, agent, devices) trusts
git, so the gate is the firewall.

## Control 1 - The nix gate is the injection firewall

Every config write runs one serialized transaction
(`internal/app/config.go:211,319-369`): decode `fleet.json`, mutate,
re-encode, write, **`gate.Validate`**, and only on success commit; on
failure the original bytes are restored and a `*ports.ValidationError`
is returned. The gate forces each affected host's toplevel derivation
`config.system.build.toplevel.drvPath` through `nix eval`
(`internal/adapters/nix/gate.go:80,245`), which runs the overlay
generator's injection-safe asserts and the full NixOS module system -
so an unknown option, a wrong type, a non-existent path or an injection
attempt fails the eval and never reaches git.

`fleet.json` values are option INPUTS to the generator, never
concatenated into nix source. The only strings spliced into a nix
expression are host-name slugs, re-validated against
`^[a-z0-9][a-z0-9-]{0,62}$` immediately before interpolation at every
splice point (`gate.go:192,247`, `build.go:77`) and formatted with
`%q`; execution is an argv slice with no shell (`gate.go:28-30`).

**Qualification (residual R1).** "The console only writes data" is not
absolute. `WriteOverlay` (ADR 0014, `internal/app/config_overlays.go:53`)
lets an owner author raw Nix module source into `overlays/<name>.nix`.
Its sole barrier is the same eval gate: a module that does not evaluate
never commits. The safety property for console-authored nix is
therefore "it must evaluate," not "console never writes nix." An owner
who can write an overlay that DOES evaluate can run arbitrary nix at
build/eval time within the gate-runner. Mitigation today: owner-only,
audited commit, per-cell blast radius; a sandboxed eval (design 0003,
gate=eval) tightens this.

## Control 2 - Token model

Secrets are 24 bytes of entropy, hashed with **argon2id** (memory
64 MiB, time 1, threads 4, keylen 32, salt 16 -
`internal/domain/token/token.go:72-79`), stored as
`argon2id$mem,time,threads$salt$key`; the plaintext is returned once
from `Mint` and never persists (`token.go:64,82-83`;
`internal/adapters/postgres/tokens.go:28-35`). Verification is
constant-time (`token.go:151`).

- **Timing.** The secret embeds its own id (`sxt_<id>_<random>`), so a
  lookup is exactly one record - no table scan, no id-space oracle
  (`token.go:106-109`). Every miss runs a full argon2 comparison
  against a fixed `dummyHash` and returns false, equalising the
  hit/miss timing (`token.go:157-170`, called from `cred.go:47`,
  `app/token.go:116,125`).
- **Ceilings narrow, never widen.** A personal token may carry a
  ceiling that clamps its effective role down (viewer|editor|owner);
  resolution does `if ceiling < got { got = ceiling }`
  (`authz.go:98-99,116-117`) - one-directional.
- **No chaining.** There is no mint-from-token primitive; a token
  projects to an `identity.User` judged by the same resolver as a
  session (one authorization path). A device/station secret is rejected
  from the operator-API path outright (`app/token.go:124-127`).
- **TTL mandatory.** `Mint` refuses `ttl <= 0` (`token.go:96-98`);
  operator tokens default to 30 days (ISO 27001 A.9.2.6, and it bounds
  group-snapshot staleness - `app/token.go:26-34`).

## Control 3 - Per-device credential, impersonation-resistant

A device credential is minted bound to its own tag (`kind=Device`,
`subject="device:"+tag`, `internal/app/device_cred.go:33-34`), stored
hash-only. The check-in path calls `AuthenticateTag(secret,
claimedTag)`, which authenticates and then asserts the proven tag
equals the claimed tag (`device_cred.go:49-52`,
`internal/http/api/checkin.go:146-154`). Device A's secret proves
subject `device:A`; reporting as B fails the equality. Re-imaging
re-issues, rotating the credential.

**R2 - CLOSED 2026-08-07, and it was worse than this said.** The shared
bridge token was accepted for *any* tag, including tags that already held a
per-device credential (`checkin.go:324-333`, and the same shape on the
station report path). So it was not a migration fallback that per-device
credentials were gradually replacing - it was a fleet-wide impersonation key
that made issuing credentials pointless while it was up. It also bought more
than "report as any device": overwriting the LUKS escrow of every device,
and acking a wipe as executed. See `docs/audit/security-2026-08.md` H1.

Closed in two steps, both measured. The bridge now covers only a subject
with no credential of its own and fails closed when that cannot be
established (0.84.0); then `SEXTANT_CHECKIN_TOKEN` was removed from the
deployment entirely. After the restart: two check-ins, both 204, zero 401s,
zero bridge lines.

## Control 4 - Read-confidentiality per scope

`fleet.VisibleTo(canView)` narrows the document a principal can read:
an org-viewer sees everything; otherwise only viewable groups/devices
are kept, policies/filters only while a kept assignment references
them, org-only filter rules are redacted to name+match-kind, and access
bindings only at visible scopes
(`internal/domain/fleet/visibility.go:30,91-103`). `canView` derives
from the live resolver after the ceiling clamp (`authz.go:107-121`);
its output is render-only, never an authorization verdict
(`visibility.go:9-11`). No role anywhere = no console access
(`identity.go:156`, `middleware.go:43`).

**Residual R3.** Enforcement is a per-handler discipline, not a central
interceptor: a handler must call `VisibleTo`/`canView`. No middleware
forces it, so a future handler that reads `Config.Fleet()` directly
would leak cross-scope. No current handler does, but the guarantee
rests on convention. Mitigation to consider: a read path that only
hands out already-filtered documents.

## Control 5 - The funnel's force-push boundary

Ring branches (`rings/<group>`) are machine-owned: the rollout engine
is the only writer, so it force-pushes them
(`internal/app/rollout.go:131-149`; `internal/adapters/git/git.go:371-382`).
`SetRef` only accepts a rev that resolves to an existing commit
(`git.go:344-366`), and the engine only ever targets `st.Target` - a
HEAD revision that already passed the gate on `main`. Build-before-
promote realises the release into the cache before the branch moves
(`rollout.go:445-454`).

**Residual R4.** The deploy credential that force-pushes ring branches
is a high-value secret with no second factor at the git layer. An
attacker holding it AND able to craft commits on the remote could point
a ring branch at malicious content. The gate protects `main`, not a
direct malicious push to a ring branch by a credential holder. Mitigate
with: a dedicated least-privilege deploy token scoped to ring refs,
credential rotation, and (future) signed/verified ring-branch commits.

## Control 6 - Remote-action privilege split

The destructive path is split so no single compromise erases a device:

1. The unprivileged agent never destroys - it only drops an intent
   marker into a spool (`agent/src/action.rs:29-115`, test
   `wipe_spools_but_does_not_destroy`). A separate **root oneshot**
   `sextant-actd` consumes it (`deploy/nixos/actd.nix`).
2. **`armWipe` defaults false and is resolved at BUILD time into
   different bash** - an unarmed host literally ships a script that
   cannot erase, only write `wipe-refused` (`actd.nix:52-55,189-195,
   241-244`). Arming requires a reviewed per-device Nix change.
3. **Lock interlock**: wipe requires the owner-only lock flag present
   (`actd.nix:196-203`, `action.rs:88-91`).
4. **Intent-as-data**: the intent is a field on the device record, an
   audited git commit the agent merely relays on check-in - no live
   command channel (`internal/domain/fleet/intent.go:13,34`).
5. **Two-step arming in the domain**: `SetDeviceIntent` refuses wipe
   unless the device is already locked or `force` is set, and refuses
   retired devices (`intent.go:19,27-30`). Arming requires **org
   Owner** (`internal/http/api/intent.go:24-25`).
6. **Replay guard** (design 0004, implemented): the wipe intent carries
   a stateless server-signed nonce + timestamp
   (`internal/http/api/intent_nonce.go`); the agent acts only when fresh
   (< 15 min) and echoes the nonce, which the server verifies before
   recording the outcome. A replayed or forged wipe response/ack cannot
   trigger or masquerade as a real erase. Stateless (HMAC, no stored
   nonce) so it holds across replicas.

So an unauthorised wipe needs all of: org-Owner authority to arm + the
audited commit, the host separately armed at build time, the device
locked first, and a fresh signed nonce. Console compromise alone is
insufficient.

**Residual R5.** The wipe unit deliberately keeps a loose
`SystemCallFilter`/`DeviceAllow` (`actd.nix:296`) - a documented choice
to avoid an untested over-restrictive sandbox breaking a genuine wipe.
Revisit once the wipe path has hardware coverage.

## Control 7 - One authorization path

Every principal (session, scoped token, break-glass static) becomes an
`identity.User` judged by one resolver that walks org + group ancestry
and takes the highest binding (`identity.go:116,179-202`;
`authz.go:48-77`). Bindings live in `fleet.json`, so access changes
ride the same gated, audited write transaction as any config change.
Console mutations enforce CSRF (constant-time, non-empty, body capped
1 MiB - `middleware.go:92-108`); API session mutations use an
`X-CSRF-Token` header; bearer/token principals are CSRF-exempt by design
(CSRF defends cookies, not bearer tokens - `authz.go:38-41,126-132`).

**Residual R6.** A personal token carries a group snapshot; a
membership removal (while the group still exists) is only bounded by the
token TTL (default 30 days). The code prunes only groups deleted from
the directory (`app/token.go:140-163`). Close with a per-user
membership adapter consulted at auth time.

## Data at rest and in logs (verified)

- Token/credential secrets: hash-only, `json:"-"`, never logged (only
  tag/kind/actor metadata).
- Device secrets: AES-256-GCM sealed; the store never handles plaintext
  (`internal/adapters/postgres/device_secrets.go:14-16`).
- All git/nix exec: argv slices, `--`/`--end-of-options` guards, no
  shell.
- No `TODO`/`FIXME`/`HACK` near any security boundary in `internal/`,
  `agent/src`, `cmd/`, `deploy/`.

**R7 - CLOSED (design 0009).** The plaintext fallback is gone: a status
report carrying a recovery key is refused with an actionable error when
no secret store is configured (`internal/http/api/station.go`,
`handleJobStatus` + `sealLUKS`), so recovery material never rests
unencrypted - the station retries once the store is configured. The
wizard path escrows the same way: the provisioning ceremony mints a
recovery keyslot (`deploy/nixos/actd.nix`), the agent uploads it once
over the authenticated check-in, the server seals it
(`internal/http/api/checkin.go`) and confirms via response header
before the device deletes its copy - no plaintext at rest on either
side, reveal stays Owner-only + audited.

## Control 8 - The public binary cache, and what a store path is worth

`cache.sextant.bb-open.com` is reachable from the internet and answers
`WantMassQuery: 1`. That is normal for a Nix cache and it is a deliberate
choice, so it is recorded here rather than left to be discovered.

**What an attacker gets from a store path.** Not much, and this was measured
rather than argued. A full `test15` system closure is 2047 paths. Grepping all
of them for the fleet's own identifiers - bind DN, LDAP search base, console
URL, directory suffix, machine-account names - returns exactly ONE hit: the
NetBird management hostname, in a wrapper script. No credential, no bind DN, no
search base, no token.

That is the escrow design paying off rather than luck. Anything that would hurt
is resolved at RUNTIME from `/run/agenix/...`, so it is never an input to a
derivation and never enters the store. The `.age` files that do live in the
store are encrypted to device host keys.

**What still holds the risk.**

- Store paths are unguessable (32 characters of base32) and the overlay that
  produces them is private, so the hashes cannot be derived by an outsider.
  Enumeration is not the exposure; a LEAKED path is.
- A leaked path yields one infrastructure hostname and a package manifest -
  which versions of which software a fleet runs. That is version disclosure,
  the same category as the `/metrics` finding, and it is the reason to keep
  this bounded rather than shrug at it.

**Mitigations, in the order they are worth doing.**

1. Keep secrets out of derivations. Already the rule; the measurement above is
   the check that it is still true, and it should be re-run when a new
   integration lands. An integration that bakes a URL or a DN into the store
   moves it from "runtime secret" to "published".
2. Put the cache behind the mesh, or behind the credential devices already
   carry. Nix substituters support netrc auth, and every device already holds a
   per-device credential, so this needs no new secret. **Built (0.79.0,
   commit `dcce76e`), not yet switched on.** The cache endpoint answers 401
   with a
   `WWW-Authenticate` challenge when the caller is not authorised
   (`cmd/gate-runner/build.go:190-216`), authorisation is either a shared
   `CACHE_TOKEN` or a per-device credential verified against the console
   (`cmd/gate-runner/main.go:563-635`), and an unreachable console **fails
   closed** (`main.go:620-621`). The chart ships it off —
   `gateRunner.cache.requireAuth: false` (`deploy/helm/values.yaml:135`) —
   deliberately, because the order is not negotiable: the netrc must be on
   every device *before* the server demands the token. A device that cannot
   authenticate does not fail loudly; it quietly builds its own closure,
   which costs hours and looks like a slow rollout rather than an access
   problem. Flipping it is acceptance-plan section A17, and it is the one
   mitigation whose *ordering* is the risk, not its implementation.
3. Do not treat the signing key as access control. It proves integrity, not
   confidentiality: a signature stops somebody serving you a tampered closure,
   it does not stop them reading yours.

**Residual.** Accepted for now at the level of "one hostname and a package
list, to whoever already has a store path". Revisit before a customer fleet
runs on it, because their manifest is their business and not ours to publish.

## Residual-risk register

| Id | Risk | Current mitigation | Close with |
|----|------|--------------------|-----------|
| R1 | Owner-authored overlay runs arbitrary nix at eval | owner-only, audited, per-cell, must evaluate | sandboxed eval (design 0003) |
| R2 | Shared bridge token impersonates any device | CLOSED 2026-08-07: token removed from the deployment; the code path additionally refuses the bridge for any subject that has its own credential | - |
| R3 | Read-confidentiality is per-handler convention | all current handlers comply | pre-filtered read path |
| R4 | Deploy force-push has no second factor | gate protects main; build-before-promote | ring-scoped least-priv token, rotation, signed refs |
| R5 | Wipe unit keeps a loose syscall sandbox | documented, wipe needs 3 other walls | harden after hardware coverage |
| R6 | Personal-token group snapshot staleness | TTL-bounded (30d) | per-user membership adapter |
| R7 | LUKS recovery key plaintext when store off | CLOSED: the store is required - a keyed report is refused without it; wizard path escrows via confirmed check-in upload (design 0009) | - |
| R8 | Shared local admin credential on every device | the hardcoded `dawo` account is superseded by the `localAdmin` card: off by default, name chosen per scope, password a secret-ref hash delivered per device via agenix, sudo asks for it, and turning the card off locks the account | retire `dawo.bootstrapUser` once every fleet has migrated |

**Each row states whether its own precondition holds.** This paragraph used
to read "none of R1-R8 is a live exploit against the deployed configuration
(store enabled, per-device credentials issued, owners trusted)". That second
precondition was untrue for months - `dawo-inspoelstraat` had no device
credential - and it was the one sentence telling a reader whether these
edges were theory or practice. A register that clears itself on an untested
assumption is worse than no register, so the blanket claim is gone.

Measured against the running fleet on 2026-08-07:

- **R1** holds as written: `WriteOverlay` has one caller and it is
  owner-only; the editor's check path only parses.
- **R2** closed, see above.
- **R3** holds: every scoped reader filters; it remains a convention rather
  than a structural guarantee.
- **R4** is understated and is tracked as audit finding H2 - the credential
  is a *person's* account, not merely a broad one. The mechanism to replace
  it shipped (ADR 0022); the machine account does not exist yet.
- **R5** not measured. It lives in the core nix, not this repository.
- **R6** real in the code, empty in practice: there are zero personal tokens
  in production, so there is no stale snapshot to go stale.
- **R7** closed.
- **R8** open by design, pending fleet migration off `dawo.bootstrapUser`.

One risk that was NOT in this register belongs here and is now tracked as
H3: device login binds over plain `ldap://`, so staff passwords cross the
cluster network in the clear. ADR 0021 settles the transport; the
certificate work is outstanding.

Re-measuring these against production is a release step, not a memory
exercise. That is the whole lesson of the sentence this replaces.

## Findings from e2e-2 (2026-07-30)

Two security-relevant facts came out of running the integrations on real
hardware; both are recorded in `docs/e2e-2-findings.md` in full.

- **A device's secrets are only as reachable as its host key is known.**
  Until the imaging flow recorded the device's SSH host public key, a fresh
  device could decrypt nothing and therefore silently ran without ANY
  integration - including the endpoint agent. The failure mode is
  fail-closed (no config rather than wrong config), but it is invisible
  without the stall guard, so a fleet could believe a device was managed
  while it was not. Host keys are now recorded at install
  (`fleet.ITAM.HostKeyID`) and secrets re-encrypted from the fleet's own
  facts (`scripts/rekey-secrets.sh`).
- **Device login binds over LDAPS; plain LDAP is an acknowledged exception**
  (ADR 0021). This bullet argued the opposite until 2026-08-06: that plain
  `ldap://` was fine because the directory sits behind the WireGuard mesh.
  That argument covered one leg of the path. A simple bind carries the end
  user's **password**, and from the mesh routing peer to the directory pod it
  crosses the cluster network in the clear - which is why the plain branch has
  to set `ldap_auth_disable_tls_never_use_in_production`, upstream's own name
  for the option. Measured on 2026-08-06: that option was set in production at
  bb-open. A deployment may still acknowledge its way onto plain LDAP, and
  then this is its residual risk, recorded in the fleet document rather than
  in a comment.
