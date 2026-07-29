# 0012 - Auto-flowing updates (the ladder as standing policy)

Status: accepted 2026-07-29 (Bram), building immediately (before e2e-2).

## Problem

Every config save currently waits for an operator to press "Roll out in
waves". Intune has no such button: config flows to devices on check-in,
and OS updates flow through rings as STANDING policy (per-ring deferral),
never operator-dispatched. Our waves read as complexity where Intune
reads as calm.

## Decision

The ladder becomes standing policy; the engine starts runs itself.

- **Auto-flow**: when the engine is idle (no active/paused/halted run)
  and commits by NON-ENGINE authors sit between the promoted pins and
  HEAD, the ticker starts a run to HEAD with the org's normal plan.
  Gate, canary ring, soak and health thresholds are untouched - only
  the manual dispatch disappears.
- **Damping**: pin/ref commits are authored by the engine; a HEAD that
  differs from the pins only by engine commits does NOT trigger a run
  (else each run's own pin commits would trigger the next, forever).
- **Off switch**: `rollout.autoFlow: false` in fleet.json returns to
  manual dispatch (some orgs will want scheduled windows only).
- **The button stays as override**: "Roll out now" = expedited
  (existing semantics: short soak, full evidence); pause/halt unchanged.
- **Change requests unchanged**: a governance-gated org still reviews;
  approval merges to main, and auto-flow carries it from there - the
  Intune analog of "rings pick up the release".

## Consequences

- Updates board reads as status, not controls: "All devices current" /
  "Release N flowing: infra done, fleet in 2h".
- Core updates (DAWO-NixOS bumps) inherit the same flow after their
  change request merges - one mental model for every change class.
- The stale-pin class of bugs (ex-ring groups keeping ancient pins)
  matters less: an idle engine keeps flowing the fleet to HEAD.
