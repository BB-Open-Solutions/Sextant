# Design 0003: gate=eval in production

Status: designed, decision made (task #37)

## Problem

The write gate (`nix eval` of the overlay flake, scoped to affected
hosts) is what makes writes safe. In production gateMode=none because:
1. the runtime image (alpine) carries no nix
2. `nix eval` on the overlay = evaluating externally-influenced code
   (flake inputs resolve to remote repos) inside the console pod

## Decision: nix in the image, hard-sandboxed eval (option a), staged

Rejected alternatives:
- **CI-gate on Forgejo (option c)**: the gate must run synchronously
  inside the write transaction (mutate -> gate -> commit); a CI
  round-trip makes every settings click take minutes and the rollback
  semantics fall apart. CI stays as a SECOND net on push, not the gate.
- **Separate gate-pod (option b)**: right isolation, but it needs an
  RPC surface, shared repo volume or bundle transfer, and its own
  lifecycle - disproportionate while the console already runs per-cell
  (one tenant per instance, ADR 0009). Revisit when cells share infra.

Chosen: option (a) with these containment properties. Blast radius note:
per ADR 0009 a console pod serves ONE tenant, and the people who can
influence the overlay flake are that tenant's own engineers - the gate
does not cross a tenant boundary.

## Implementation

1. **Image**: switch runtime base to nixos/nix's static nix binary:
   copy `nix` (static build, ~30MB) into the alpine stage; keep image
   non-root. `/nix` store on an emptyDir (ephemeral cache is fine; first
   gate after a pod restart warms it).
2. **Eval flags** (adapters/nix/gate.go already shells nix; add):
   `--option sandbox true --option restrict-eval false` is NOT enough -
   use `--option allowed-uris` pinning to the two input hosts
   (code.overheid.nl, forgejo.bb-open.com) so an edited flake cannot
   exfiltrate via arbitrary fetchurl at eval time. Plus:
   `--option max-call-depth 100000 --option eval-cache true`, timeout
   already enforced by the adapter's context (verify: 120s).
3. **K8s containment** (helm):
   - the CNP already default-denies; add the two git hosts explicitly,
     REMOVE world:443 once allowed-uris lands (defense in depth pairs)
   - resources: gate evals are memory-hungry -> bump limits (1Gi) and
     document that a gate OOM = 422 to the user, not a crash (run nix
     as a child process; it dying must not kill the server - it doesn't,
     exec.CommandContext)
4. **Config**: gateMode=eval in the platform helmrelease; keep
   gateMode=none as the documented fallback.
5. **Rollback safety**: applyTx already restores fleet.json on gate
   failure; no change.

## Files to touch

- Dockerfile (runtime stage: + static nix, /nix emptyDir mount point)
- deploy/helm/templates/deployment.yaml (emptyDir volume, resources)
- deploy/helm/values.yaml (gateMode default stays none; document eval)
- internal/adapters/nix/gate.go (allowed-uris option, configurable via
  SEXTANT_GATE_ALLOWED_URIS; empty = no restriction for dev)
- platform repo: CNP egress tightening + helmrelease gateMode: eval
- ADR 0012 documenting this decision (copy the Decision section above)

## Test plan

- gate_e2e_test.go already proves eval catches bogus keys; add a test
  that allowed-uris blocks an out-of-list fetch (flake with an extra
  input -> gate must fail closed)
- prod verify: settings write with a typo'd key -> 422 with the nix
  error naming the key; valid write -> commit; time the p95 (< 15s warm)

## Acceptance

A production settings write that would not evaluate is rejected with
the module system's error before anything reaches git; eval cannot
fetch from hosts outside the allow-list; pod survives gate OOM.
