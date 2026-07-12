# Sextant handoff (Fable -> Opus)

Purpose: everything needed to finish the remaining 1.0 work without
re-deriving context. Written at the end of a Fable build session.

## Where the product stands

Live at console.bb-open.com (0.11.0). M1, M2, M3 complete. Overlay
cutover done (sextant-overlay-bbopen carries the v3 flake; the whole
DAWO core + Sextant addon evaluates; funnel proven: dawo-inspoelstraat
follows rings/pilot). Evidence export shipped. CI fixed (Go + agent +
nix). Repo blind-spot sweep done.

Two review agents (code + security) have vetted every M3 surface; all
fixes landed. The code is senior-grade: hexagonal, pure domain fully
tested, adapters behind ports, thin transport, race-clean, gofmt/vet/
clippy clean, OpenAPI contract-tested.

## Architecture in one screen

- `internal/domain/*` pure, no I/O: fleet (resolver, policy compiler,
  filter eval, mutations, visibility, catalog), identity (roles,
  bindings, prefs), change, rollout, token, observed.
- `internal/app/*` use-case services over ports: ConfigService (safe
  write tx: mutate -> gate -> commit -> push), ChangeService,
  RolloutService (+funnel WithRefs), InventoryService, TokenService,
  DeviceCredentials, EvidenceService.
- `internal/ports` interfaces; `internal/adapters/{git,nix,postgres,
  oidc,ldap}` implement them.
- `internal/http/{api,web,mw}` transport; `cmd/{sextant,sxctl}`.
- `agent/` Rust device agent. `nix/` generator + resolver twin +
  catalog export + eval-assertion tests. `deploy/{helm,nixos,docker}`.
- ADRs in `docs/adr/` (0001-0011). Build-ready designs in
  `docs/design/` (0001, 0003, 0004).

Non-negotiables (enforced, keep enforcing):
- data-not-code: the console never writes nix, only fleet.json; the nix
  gate is the validator (ADR 0005).
- one authorization path for humans/tokens/machines (ADR 0008); RBAC is
  derived per request from the live document, never stored.
- every change is an audited git commit; no device RCE.
- API additive-only after freeze; OpenAPI contract test guards it.
- each capability: pure domain + tests, ports, then transport.

## What Opus should pick up (in order)

### 1. Remote actions - lock + cryptographic wipe (docs/design/0004)
Deferred here on purpose (destructive execution path). Design is
complete and build-ready. Intent-as-data + check-in-response delivery +
a tiny root actd unit split from the unprivileged agent. This is the
one piece explicitly held for the handoff.

### 2. gate=eval in production (docs/design/0003, task #37)
Static nix in the image, allow-listed sandbox, CI as second net. The
decision is made and written; it is a build. Blast radius is bounded by
cells (one tenant per pod, ADR 0009). Add ADR 0012 from the design's
Decision section.

### 3. SB + TPM2 enrollment wizard (docs/design/0001, task #35)
Detect-don't-declare posture via the agent; per-device stepwise
checklist. Touches agent (posture probe), observed plane (2 columns,
migration 0004), and the device page. Build-ready.

### 4. Threat model doc (roadmap E)
Not started. Should cover: the nix gate as the injection firewall, the
token model (argon2id, ceilings, no chaining, DummyVerify timing),
per-device credential impersonation resistance, read-confidentiality,
the funnel's force-push trust boundary, and the remote-action privilege
split. Reference the security reviews already done (in git history /
this session's commits).

### 5. Cells + admin plane (ADR 0009, roadmap F)
Instance-per-tenant provisioning over the platform GitOps repo; a thin
global admin status dashboard. Largest remaining architecture piece.
Design not yet written - Opus should write a docs/design/ entry first.

### 6. Load test + fleet.json schema-migration story (roadmap F)
Scale target 1M devices across cells. The check-in path and rollout
convergence are already SQL-aggregate (no per-device iteration in app
code); prove it. Version fleet.json (v3 -> v4) with a migration path.

### 7. Backup/restore drill (roadmap B) + operator/admin/IdP docs.

## Ops tail (small, mostly outside this repo)

- **task #29**: dedicated LDAP read-only bind account + secret in ns
  sextant, then the directory picker works live. Helm keys now exist
  (ldap.* in values). App runs fine without it (free-text bindings).
- **task #34 follow-ups** (overlay adoption): enroll the t495s with
  hardware=lenovo-t495s, write its credential to
  /var/lib/sextant-agent/credential on the device. Give the console's
  Forgejo deploy credential force-push on rings/* (funnel needs it).
- Dedicated Forgejo deploy token replacing bbadmin netrc.
- forgejo-registry-pullkey secret missing in ns sextant (pulls work
  anonymously today).
- sextant-checkin.nix in inspoelstraat-appliance -> replace the curl
  heartbeat with sextant.nixosModules.agent (phase 2 with zaanstad).
- Old sextant-nixconsole deployment cleanup (if still present).

## Release (task #19) - LAST, needs Bram's explicit go

1.0.0 -> code.overheid.nl/MinBZK/DAWO-Sextant WITH full history, via a
Forgejo push mirror. Per the push policy: only under bram.buijs, never
MinBZK without Bram's go. Do NOT do this until everything above is green
and Bram says go.

## Deploy playbook (proven this session)

1. bump deploy/helm/Chart.yaml + values.yaml, commit, push bbopen
2. `podman build -q -t forgejo.bb-open.com/bb-open/sextant:<v> . &&
   podman push ...` (watch disk: `podman image prune -f` if full)
3. platform repo apps/sextant/helmrelease.yaml tag -> `git push origin
   main` (NOT the github-mirror remote - that was a real trap)
4. `flux reconcile source git flux-system -n flux-system` then
   `kustomization sextant` then `source git sextant` then
   `helmrelease sextant -n sextant`
5. verify: rollout status, image tag, /readyz, smoke the new surface

## Commit / style rules (keep exactly)

English, ASCII-only, Conventional Commits, no AI-marketing. Trailer on
every commit: `Code AI-assisted (Claude Fable 5); testing, review and
integration by a human.` Opus continues the same trailer with its own
model name. Mirror to forgejo bbopen remote after each commit.
