# ADR 0011: Update funnel via machine-owned ring branches

Status: accepted (2026-07-10)

## Context

Staged rollout needs a mechanism, not just data. The rollout engine
commits `groups.<g>.pin = <rev>` per promoted ring, but devices converge
with comin from a git branch: nothing made a pinned ring's devices
actually receive the target revision, or made unpinned devices hold
back. A pin that only exists in `fleet.json` at HEAD cannot steer a
device that has not pulled HEAD yet - and pulling HEAD is exactly what
staging must prevent.

## Decision

Every rollout ring group gets a **machine-owned branch** `rings/<group>`
in the overlay repository. Devices whose group ancestry intersects a
ring (first match in plan order) converge from that branch instead of
`main`; the generator emits the choice as the pure bridge option
`sextant.cominBranch`, which the DAWO core wires into `services.comin`.

The rollout engine is the **only writer** of ring branches:

- **Promotion**: pin commit on main first (the audit record), then the
  ring branch moves to the target revision and is force-pushed.
- **Idle** (no active run, ring unpinned): the engine's tick loop
  fast-forwards ring branches to HEAD, so ordinary commits still reach
  every device without a rollout run.
- **Pinned** rings never follow: a halted or cancelled rollout holds
  its rings exactly where they were - config is truth, a human decides.

Force-push is deliberate: ring refs are not human branches, and a
re-targeted rollout may legitimately move one backwards.

## Consequences

- Devices outside every ring behave as before (follow `main`).
- The gate never runs for ring-branch moves: refs only ever point at
  commits that already passed the gate on main.
- The overlay's git server must allow the console's deploy credential
  to force-push `rings/*` (and nothing else needs force).
- Removing a group from the ring plan leaves its branch behind; devices
  return to `main` on their next generation, and the stale ref is
  harmless (cleanup can come with cell provisioning).
