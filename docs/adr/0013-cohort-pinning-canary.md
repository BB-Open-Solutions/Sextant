# 0013 - Cohort pinning: count-capped canary within a wave

## Status

Proposed. Slice 1 (domain model + deterministic cohort selection) implemented;
the engine + generator wiring (slice 2) is not yet built.

## Context

Today a rollout wave is a device group: promoting a wave pins the whole group
(`SetGroupPin`) and moves its `rings/<group>` branch (ADR 0011), so every device
in the group receives the target at once. Progressive rollout ("first 1, then
10, then 100") is expressed by sizing groups and ordering waves.

Operators also want a count-capped canary WITHIN a single group: release the
update to the first N devices of a wave, let it soak healthy, then widen to
the next N, independent of how the group is sized.

## Decision

Add an optional `maxDevices` cap to a wave, and release its group's devices in
deterministic cohorts. A wave with `maxDevices = 1` releases one device first;
the engine widens the released cohort as each converges healthy through soak,
up to the whole group.

### Slice 1 (this change): domain, pure and tested

- `rollout.Ring.MaxDevices` (mirrored on `fleet.RolloutRing`): 0 = the whole
  group (today's behaviour), N > 0 = release at most N at a time.
- `rollout.Cohort(sortedDevices, released)` selects the released prefix - a
  pure, deterministic function over a sorted device list, so the same devices
  are always chosen and the selection is auditable.

### Slice 2 (next): engine + generator wiring

- The generator's `ringBranchFor` releases a device onto its ring branch only
  when the device is in the ring's released cohort. Cohort membership must be
  config-as-data (fleet.json) for the pure generator to see it - a per-device
  release marker the engine writes, honoured before the group-based branch.
- `rollout.Decide` grows the released cohort (by `MaxDevices`) once the current
  cohort is healthy through soak, before advancing to the next wave.
- Convergence (`RingStatus`) counts the released cohort, not the whole group.
- Update the Go<->Nix parity harness for the cohort-aware branch decision.

## Consequences

- Backwards compatible: `maxDevices = 0` is exactly today's whole-group wave.
- The wave editor gains a "max devices" field only once slice 2 enforces it
  (the console never shows a control that does nothing).
- More fleet.json churn during a capped rollout (a commit per cohort step),
  bounded by the number of cohort widenings, not devices.
