# Security audit, August 2026

Read against `main` on 2026-08-06, before 1.0.0 and before Zaanstad. Every
finding cites `file:line` and was measured against running production
wherever that was possible. Findings without evidence are not in here.

Method: read adversarially per path, not per file. Where a path crossed a
claim in `docs/threat-model.md`, that claim was held against the code.

## Findings

### H1 - The shared bridge token is on, and the station cannot do without it

**What.** `POST /api/checkin` accepts two proofs of identity
(`internal/http/api/checkin.go:324-333`). The first is a per-device
credential bound to the tag - device A's credential is refused for tag B,
which is correct. The second is a **shared token bound to nothing**: whoever
holds it may check in as any tag.

Measured in production: `SEXTANT_CHECKIN_TOKEN` is in the secret
`sextant-v2` and reaches the pod through `envFrom` (64 characters, set).

**Why this is a finding and not a known residual risk.** `threat-model.md`
describes it as R2 and names the closing condition: *"retire the shared
bridge token once every device is enrolled with its own credential"*.

That condition is **not** met, and it is worth knowing which device holds it
open. Measured:

- `e2e5` has `device-e2e5` and checks in over the strong path.
- `dawo-inspoelstraat` has **no** device credential. It has a
  `station-dawo-inspoelstraat` of kind `station`, and `authenticateBound`
  refuses on `tok.Kind != want` (`internal/app/cred.go:46`) - so a station
  credential does not authenticate a device check-in.
- The station checked in today at 19:14 (`device_status`).

It follows that the station authenticates with the **shared token**.
Removing it breaks its check-in immediately.

The cause is not the credential but the lifecycle: the station is
*registered* as a station and was never *imaged* as a device, so it never
received the credential the imaging station hands out. Same root as why it
has no netrc for cache authentication.

This correction first stood the other way round in this document ("that
condition is met"), on the strength of the agent source rather than the
running fleet. It reads this way because a security document that is wrong
is more dangerous than no document.

**What it gives whoever holds it**, and this is more than R2 describes
("can report as any device"):

1. **Overwrite any device's escrow key.** A check-in carrying a
   `recoveryKey` field writes through to `device_secrets` with
   `ON CONFLICT ... DO UPDATE SET ciphertext=EXCLUDED.ciphertext`
   (`internal/adapters/postgres/device_secrets.go:28-33`). There is no state
   check - only a 256 length bound. The device erases its own copy as soon
   as the server returns `X-Recovery-Key-Stored: 1` (`checkin.go:283-289`),
   so the escrow is the only copy. One request per device makes the whole
   fleet's LUKS recovery keys useless.
2. **Forge a wipe as executed.** The wipe intent rides on the check-in
   response, including a server-signed nonce (`checkin.go:305-311`). Whoever
   checks in as tag X receives that nonce and can echo it back as an ack on
   the next beat. `verifyIntentNonce` succeeds, because the nonce is
   genuine. Consequence: the console reports a stolen laptop as wiped while
   the machine is intact. The replay guard protects against reuse of an old
   nonce, not against somebody who can authenticate as the device.
3. **Forge compliance statements** - posture, systemd health and revision
   are all self-reported.

**Severity: high, but bounded.** Exploiting it requires the token, and that
lives only in the cluster secret, not on devices. Whoever can read that
secret has cluster access and therefore larger options already. It is still
worth doing because it is a free removal that closes a whole attack surface,
and because consequence 1 coincides with the missing Postgres backup: the
escrow keys have no second copy.

**Advice, in this order - the first step cannot be skipped.**

1. Give the station a device credential of its own. Until that happens,
   removing the shared token locks the imaging station out.
2. Then empty `SEXTANT_CHECKIN_TOKEN` **and** remove the fallback in
   `authorized()`, so a token put back by accident does not reopen it.
3. Close R2 in the threat model, and name consequences 1 and 2 there - while
   R2 says only "report as any device" it reads as a nuisance rather than as
   loss of recovery keys.

Step 1 touches the imaging station and therefore hardware; this is not a
change to roll out unattended.

**Status 6 August, 23:00 - steps 1 and 2 done, with a discovery in between.**

The station now has its own device credential. Measured: the token
`device-dawo-inspoelstraat` was last used at 21:00:06.06 and
`device_status.last_seen` reads 21:00:06.11 - the same check-in, so the
station authenticates as itself. For `e2e5` that drift is zero too. Neither
device leans on the bridge any more.

Carrying out step 2 showed the fallback to be worse than described above.
`authorized()` tried the device credential first and fell back to the shared
token on EVERY failure - including for a tag that had held its own
credential for weeks. The token was therefore not a migration path but a
fleet-wide impersonation key: issuing credentials changed nothing about the
exposure while the bridge was up. All three consequences above applied in
full to `e2e5`, which was already using the "strong path".

The same held for `POST /api/station/{tag}/report`
(`internal/http/api/station.go`), which additionally had no test at all on
the bridge token - the reason it went unnoticed.

Both paths now accept the bridge only for a subject **without** a credential
of its own, and fail closed when the token store cannot answer that
question. What remains is a warning, throttled to once an hour per subject,
so that the answer to "is anything still using this token" is a measurement
rather than a guess. Two tests per path; both were verified by removing the
guard and watching them go red.

**Closed 7 August, 00:00.** `SEXTANT_CHECKIN_TOKEN` has been removed from
the secret `sextant-v2` and the console restarted. The value was
deliberately NOT kept: if something turns out to lean on it, the right
repair is to give that thing a key of its own, and a copy makes the wrong
repair too easy.

Measured over the first minutes after the restart: two check-ins, both 204,
exactly sixty seconds apart, **zero** 401s and **zero** bridge lines.

Step 3 done 7 August: R2 is closed in the threat model, and the register no longer clears itself on an untested precondition (see M2).

### M1 - Thirteen valid credentials for devices that no longer exist

**Measured in production.** `api_tokens` holds **15** unexpired device
credentials; `fleet.json` holds **2** devices. Thirteen belong to tags that
were removed from the fleet. They were minted on 2026-07-13 and expire on
**2031-07-12**: `boundCredTTL` is five years (`internal/app/cred.go:19`), on
the reasoning that devices are long-lived and rotate on re-imaging rather
than expiring. That holds for a device that continues to exist.

**Why they are still there.** Not because revocation was forgotten: both
removal paths call `Revoke` (`internal/http/web/device_ops.go:103`,
`internal/http/api/handlers.go:156`). They are there because the credential
lifecycle hangs off those two handlers and **not off the fleet document**.
Every other way a device leaves `fleet.json` - a change request, a commit in
the overlay, a restore - leaves the credential behind, and nothing
reconciles that afterwards.

**Impact, honestly bounded.** A credential is bound to its own tag, so it
cannot impersonate an existing device. What it can do is check in as a ghost
device and inject observations for a tag that is not in the fleet. That is
exactly the picture that was on the overview this morning: removed devices
reporting back. This is the mechanism that keeps producing them.

**This is the third time today the same pattern appears**, and that is the
real finding: the configuration plane and a side store drift apart, and
there is no reconciliation.

| | side store | found |
|---|---|---|
| Ghost devices on the overview | observed plane | fixed, `7cf2558` |
| Orphaned branches of withdrawn changes | git | fixed, `32aa7bd` |
| Credentials of removed devices | token store | **open** |

The first two were solved by filtering at read time and by sweeping at
start-up respectively. For this third one the second shape is the obvious
fit: at start-up, revoke every device credential whose tag is not in the
fleet document, and say so loudly.

**Advice.** Revoke the thirteen now, and build the reconciliation so it
cannot creep back. Consider lowering `boundCredTTL`: five years is long for
something whose revocation can demonstrably fail.

**Status 6 August, 23:00.** The reconciliation was built
(`DeviceCredentials.ReconcileWithFleet`) and shipped in 0.83.0 - but it did
not run. The call sat behind `if d.devCreds != nil`, forty lines above where
`devCreds` is assigned, so the guard always found nil. Measured after the
deploy: still 15 credentials against 2 devices, and no log line.

That is the same class of error as the rest of this document, this time in
the repair itself: a green test over the wrong layer. The test covered the
service, not the wiring. The call now sits in the Postgres block without a
guard - the dependency exists there by construction, and moving it back
earns a loud start-up panic - and it logs every time, including
`revoked=0`, so "did nothing" and "never ran" no longer look alike.

**Closed 7 August, 00:00.** 0.84.0 is running and the sweep ran at start-up:
**14 orphans revoked**, each named with its tag, followed by
`device credentials reconciled against the fleet revoked=14`. `api_tokens`
goes from 16 to 2 device credentials, equal to the number of devices in the
fleet document.

### H3 - Staff passwords cross the cluster network in the clear

**Measured.** `identity.ldapUri` in `fleet.json` reads `ldap://10.43.76.5`.
On that branch the overlay module sets three things
(`modules/integrations.nix:334-357`):

```nix
ldap_tls_reqcert = "never";
ldap_auth_disable_tls_never_use_in_production = true;
ldap_id_use_start_tls = false;
```

The middle one is not our naming; that is what SSSD calls the option. It is
on, on running hardware.

**Why this counts.** An SSSD simple bind carries the **end user's
password**, not a hash and not a token. The reasoning for why that was
acceptable (route decision 2026-07-27) was that the directory is only
reachable through the WireGuard mesh. That covers the device-to-cluster leg.
From the routing peer to the OpenLDAP pod the traffic is plaintext on the
pod network, and anything that can capture there - a compromised sidecar, a
node, `kubectl debug` - reads passwords as they are typed.

**Severity: high.** This is the strongest credential in the system, for
every member of staff who logs in, and it is not bounded to whoever already
has cluster access: capturing on the pod network is a lower bar than reading
a secret.

**Advice.** ADR 0021 settles it: LDAPS is the supported transport, and plain
LDAP must be explicitly acknowledged.

**Status 6 August, 23:50.** The module enforces it (overlay `db23306`).
Measured both ways on `dawo-inspoelstraat`: without the acknowledgement the
evaluation refuses with a message naming the option, with it the evaluation
succeeds and the warning appears.

bb-open now has the acknowledgement on, because the directory runs on
`ldap://10.43.76.5` today. **That does not close the finding** - it makes it
visible, and puts it in the fleet document instead of in a comment. Closing
it requires a certificate on the OpenLDAP service and then `ldaps://`; that
is platform work. The console's own bind
(`ldap://openldap.ldap-bb-open:389`) moves in the same step - it carries the
`cn=sextant-ro` password, narrower but no different in kind.

### H2 - The console pushes as a person, not as a machine

**Measured.** The netrc in the console pod authenticates to
`forgejo.bb-open.com` with `login bram.buijs`. That is the credential the
rollout engine force-pushes ring branches with, and with which every commit
from the console reaches the forge.

**Why this is more than R4 says.** `threat-model.md:151-157` describes R4 as
"no second factor at the git layer" and advises *"a dedicated
least-privilege deploy token scoped to ring refs"*. The reality is not "not
least-privilege yet" but "a human being's account":

1. **Blast radius.** The credential carries everything that person may do on
   the forge, not only ring refs. Whoever reads the pod has that.
2. **The audit trail lies.** Every automatic push - every ring promotion -
   appears in the forge under the name of a person who may have been doing
   nothing at the time. "Who moved this ring" is therefore unanswerable, and
   that is exactly the kind of question an audit trail exists for.
3. **Key-person dependency.** If that person leaves, or the password
   rotates, the fleet stops rolling out until somebody notices.

For a municipality, "the automation uses the lead developer's account" is a
finding in any ISO 27001 assessment, regardless of whether it ever goes
wrong technically.

**Advice.** A machine account on the forge with write access to this one
repository, and the netrc pointing at it. That is not much work and it
solves all three points at once. Rewrite R4 to say what is there rather than
what it ought to be.

**Status 7 August, 01:00 - mechanism built, finding NOT YET CLOSED.**

ADR 0022. The console can store its own forge credential (sealed, per
tenant) and writes the netrc onto its own volume. An admin replaces it at
`/org/forge`; git reads the file per invocation, so it takes effect on the
next push with no restart. Who replaced it and when is recorded. With
nothing stored, nothing changes: the mounted secret still governs.

What closes the finding is not the mechanism but the use: a machine account
has to be created on the forge with write access to the overlay repository
only, it has to be entered, and a real change has to be pushed under that
name. Until that push, H2 is open and bb-open still pushes as a person.

### M2 - The threat model clears itself on a precondition that does not hold

`docs/threat-model.md:310-312` closes the risk register with:

> *"None of R1-R8 is a live exploit against the deployed configuration
> (store enabled, **per-device credentials issued**, owners trusted)"*

That second condition is untrue. `dawo-inspoelstraat` has no device
credential (see H1), and that is precisely the condition on which R2 is
called "no live exploit". The sentence is right for `e2e5` and wrong for the
fleet.

This is not a wording issue. It is the one sentence in the document that
tells a reader - an auditor, a municipality, a colleague - whether the
listed edges are theory or practice, and it had never been held against the
running environment.

**Advice.** Replace the sentence with something that states per risk whether
its precondition holds, and make that check part of the release rather than
of somebody's memory. A register that does not test its own assumptions ages
exactly the way `1.0-fit-gap.md` did.

**Closed 7 August.** The blanket claim is gone from `docs/threat-model.md`;
the register now states per risk whether its precondition holds, measured on
7 August, and says plainly which one (R5) was not measured and why. H3 is
listed there as a risk the register never had.

### L1 - A line reference in the threat model has expired

`docs/threat-model.md:114` cited `checkin.go:150-153` for the shared-token
comparison; it now lives at `checkin.go:324-333`. Small, but it is the same
kind of drift that had `1.0-fit-gap.md` asserting wrong things for two
weeks. A reference that no longer holds slows the next reader down, and
makes the one after that distrustful.

**Closed 7 August**, together with M2: the paragraph carrying that reference
was rewritten when R2 closed, and the reference went with it.

### L2 - One write path takes a settings key the catalog has never heard of

**Measured.** Three paths write a setting, and they do not agree on whether
the key has to exist:

- The settings page iterates `cat.Entries` and reads only `v:<name>` fields
  (`internal/http/web/settings.go:399`), so an unknown key cannot appear. The
  guarantee is structural.
- The API looks the key up and refuses what it does not find
  (`internal/http/api/handlers.go:215`).
- **The device page does neither.** `postDeviceSetting`
  (`internal/http/web/devices_page.go:580`) takes a free-form `key` form
  field and writes it straight through the gate.

**Impact: low, and worth saying why it is not nothing.** This is not a
privilege escalation - the handler is Editor-scoped on that device like every
other write, and the nix gate still has to accept the resulting document. The
cost is that a typo becomes a setting that governs nothing, stored in the one
document whose entire purpose is to state what governs. An operator reading
`apps.ofice: true` on a device page has no way to tell it from a setting that
works, and neither has the next reviewer.

It is the same shape as R3: a rule two paths enforce and a third relies on
nobody exercising.

**Advice.** Have `postDeviceSetting` look the key up the way the API does.
Deliberately NOT done at the moment of discovery (02:00, 2026-08-07): it
tightens a write path, and if a deployment's `catalog.json` is incomplete it
would start refusing writes an operator has been making.

**CLOSED 2026-08-07, in daylight and after checking.** The precondition was
measured first: every setting stored at org, group and device scope in the
bb-open fleet document resolves against the 70-key catalog, so tightening
this refuses nothing anybody is doing.

The fix turned out to have two halves, not one. Besides the key, the API
also parses the VALUE through the catalog entry's type, and this handler was
guessing the type from the string's shape. That is how a list-valued setting
once became a one-element list holding the text "[a b]" - and
`usbDevices.allowlist` is list-valued, so the silent case reconfigured a
security control. Both halves now go through the catalog.

The test was inverted rather than deleted, and the guard was verified by
forcing the lookup to succeed and watching it fail.

## Checked and sound

- **Authorisation per request.** Every mutating web handler calls
  `requireWeb`, directly or through `requireDeviceEditor`
  (`internal/http/web/device_ops.go:38`). A first scan flagged four
  destructive device handlers as a false alarm; those use the helper.
- **Tokens.** argon2id, stored hash-only, constant-time comparison
  (`internal/domain/token/token.go:149-151`), a mandatory positive TTL - no
  eternal tokens - and the secret carries its own id, so verification looks
  up exactly one record instead of scanning the table.
- **Device identity along the strong path.** A per-device credential is
  bound to its tag and refused for another tag, with that intent explicit in
  the comment.
- **Wipe-ack replay.** Signed nonce plus timestamp, and an ack that fails to
  verify leaves the beat standing but discards the outcome
  (`checkin.go:256-262`) - so a forged ack does not pollute the audit trail.
  That is the right order.
- **Retired devices.** A retired tag gets 410 before any processing
  (`checkin.go:245-249`): lifecycle beats authentication.
- **The nix gate as an injection firewall.** Hostnames are checked against
  `hostRe` at the splice point itself and quoted with `%q`
  (`internal/adapters/nix/gate.go:254-264`), with the reason explicit in the
  comment: *"this function must not trust that a caller upstream did its
  job"*. Execution goes through an argv slice, no shell. Setting values
  reach nix as JSON data, not as an expression.
- **CSRF, structurally.** All 67 POST routes go through one wrapper
  (`internal/http/web/web.go:146-148` -> `action`), which compares in
  constant time (`middleware.go:113`). No route registers outside that
  helper, so this is not a convention a new handler can forget - the
  difference with R3, where read confidentiality *is* agreed per handler.
- **Read confidentiality (R3).** The claim "all current handlers comply"
  holds. Every API reader that returns scoped data filters through
  `VisibleTo` or `canView`; the four handlers that do not are self-scoped
  (`getMe`, `getMyPrefs`, `getTokens` - the last listing only
  `p.user.Subject`) or require Owner (`getDirectoryGroups`). It remains a
  convention rather than a structural guarantee - unlike CSRF, which runs
  through one wrapper - but it is honoured today.
- **Path traversal.** `Repo.safePath` (`internal/adapters/git/git.go:121-140`)
  does it in two layers: lexically first (`filepath.Rel` plus a `..` prefix
  check), then **resolving symlinks** on the nearest existing ancestor and
  re-confirming the path stays under the repo root. That second layer is
  where most implementations fail: a lexical check alone is defeated by a
  symlink. This is the path the overlay editor (ADR 0014) writes through, so
  it is exactly where it needs to be. `changeFile` validates the id before
  the join (`internal/adapters/state/state.go:113-118`), and the secret
  reference in `cmd/sextant/capabilities.go:283` uses `filepath.Base`.
- **Size limits.** Every request with a body runs through
  `http.MaxBytesReader`, with a bound per path: 4 KiB for device auth,
  320 KiB for a check-in, 4 MiB for a station report and a diagnostics
  bundle. No decode path was found without a bound.
- **Outbound connections.** The only destination an operator can set is the
  SMTP host (`internal/http/web/mail.go:43-45`), and that is org-Owner-only.
  An Owner can rewrite the entire fleet configuration anyway, so this is not
  a privilege escalation. The gate-runner URL, the OpenBao address and the
  LDAP URI come from deployment configuration, not from the console.

## Residual risk register, measured

R2 and R4 are covered above (R2 became M2, R4 became H2). The rest, measured
on 2026-08-06:

**R1 - "an owner can have arbitrary nix evaluated" - HOLDS, and the wording
is right.** `WriteOverlay` (`internal/app/config_overlays.go:53`) has
exactly one caller, `web/overlays.go:99`, and it sits behind
`requireWeb(v, "org", identity.Owner)`. The second path that looked
suspicious is the editor's check button, `/overlays/check`: equally
owner-only, and it evaluates nothing. The runner runs
`nix-instantiate --parse` there (`cmd/gate-runner/main.go:628`), so parsing
only. The one place console-authored nix truly evaluates is the gate at
commit time, and that commit is in the audit trail - which is what R1's
mitigation says too.

**R5 - loose syscall sandbox on the wipe unit - NOT MEASURED.** It lives in
the core nix, not in this repository; it belongs to the hardware round.

**R6 - stale group snapshot in a personal token - NO EXPOSURE TODAY.** The
code matches the description: pruning happens only for groups that have
disappeared from the directory (`internal/app/token.go:149-162`), and
membership-removed-while-group-exists stays bounded by the 30-day TTL.
Measured in production: `api_tokens` holds **zero** tokens of kind
`personal`. Only 16 device tokens and 1 station token. The risk is therefore
real in the code and empty in practice; it becomes something the moment the
first personal token is issued.

R7 is marked CLOSED and was not re-checked.

## Where this leaves things

Six findings, two of them high, none critical, and by the close of 6 August
H1 and M1 were shut with a measurement behind each.

The picture is a codebase that does the hard things well - path traversal
with symlink resolution, CSRF structurally rather than per handler, argon2id
with constant-time comparison, a gate that does not trust its own callers -
and that stumbles over lifecycle: things that keep existing after the reason
for them is gone. H1, M1 and H2 are each an instance, and it is the same
shape as two bugs found the same day independently of this audit.

That is a more reassuring kind of weakness than the reverse. A design with a
hole in its authentication is not repaired by tidying; tidying is what this
asks for.

H3 is the exception to that comfort, and it is the one to carry into
Zaanstad: it is not a leftover but a transport decision that was wrong on
the day it was made, and it is still live.
