# ADR 0012: Remote gate-runner for production validation

## Status
Accepted (2026-07-12).

## Context
The validation gate is the safety property of the write path: no configuration
reaches git unless the overlay generator's asserts and the NixOS module system
accept it (see `internal/adapters/nix/gate.go`, ADR 0005). The gate is a
`nix eval` of the affected hosts' `toplevel.drvPath`.

The production console image is a ~46 MB Alpine binary with **no nix**. It has
therefore run live with `--gate=none`: direct edits (org/group/device settings
through the UI or API) commit **without** being validated against the generator.
Change requests are still gated by the heavier `nix build` in CI, and the nix
module on each device rejects malformed config, but direct edits had no
pre-commit gate.

Options considered (see design 0003):
1. **nix in the console image** — simplest, but adds hundreds of MB / GBs and
   pulls a general-purpose builder into the request-serving pod. Rejected: it
   defeats the small, sovereign image and widens the console's attack surface.
2. **CI-only gating** — accept `--gate=none` for direct edits. Zero work, but
   leaves a real hole: a bad direct edit is only caught at CR/merge or on the
   device.
3. **Separate gate-runner** — a small nix-capable service the console calls.

## Decision
Adopt option 3. A new `--gate=remote` mode makes the console delegate
validation to a **gate-runner** over HTTP:

- `cmd/gate-runner` keeps a warm clone of the overlay repo and a persistent
  `/nix` store. Per request it fetches+resets to the tracked branch, drops in
  the candidate `fleet.json`, and runs the existing `EvalGate` against that
  tree. One evaluation at a time (a single working tree).
- `internal/adapters/gate.RemoteGate` (a `ports.Gate`) reads the candidate
  `fleet.json` the write path just wrote and POSTs it to the runner. It is
  **fail-closed**: an unreachable or erroring runner rejects the write rather
  than committing it unvalidated. A `422` / `{ok:false}` is the generator's
  rejection and surfaces as a `ValidationError`; a `5xx` or dial failure is an
  infrastructure error, distinct from a config rejection.

Only `fleet.json` is sent because the write path only ever mutates that file;
the runner's own clone supplies the generator and modules.

Deployment: `gateRunner.enabled` in the chart adds the Deployment, Service,
PVCs (data + nix store) and CiliumNetworkPolicies. The runner is never exposed
via ingress — only the console reaches it (port 8090).

## Consequences
- Console image stays small and nix-free; the nix runtime is isolated in a
  service that serves no user traffic.
- Direct edits are validated again, closing the `gate=none` hole.
- Fail-closed means the runner is now on the write path's critical path: if it
  is down, writes are refused (correct for a security gate, but it must be
  healthy before flipping the console to `remote`).
- First evaluation is slow (clone + nixpkgs/core-flake warm); the persistent
  `/nix` PVC amortises it. Requires the overlay's flake inputs to be reachable
  and evaluable from the cluster.
- The heavy CR **build** gate still needs nix; with the console nix-free it is
  a no-op in-pod until/unless the runner grows a build endpoint. CI remains the
  build gate.

## Rollout
Deploy the runner (`gateRunner.enabled=true`) with the console still on
`gateMode=none`; verify `/healthz` and a test `/validate` against the real
overlay; only then flip `gateMode=remote`.
