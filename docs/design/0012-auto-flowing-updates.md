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

## Risk brake (decision 2026-07-29)

Not every change should flow unattended. A save whose blast radius is
large enough to want a human watching it carries a marker into its
commit subject, and the engine holds auto-flow behind it.

- **Marker**: the console appends ` [risk:high]` (`app.RiskHighMarker`)
  to the commit subject when any changed key is a catalog option with
  `riskClass: "high"`, or an integration enable (`<integration>.enable`:
  the device joins or leaves a mesh, a directory, a SIEM).
- **Hold**: the auto-start walk stops at a marked non-machine commit
  above the pins - no run starts, and the owning groups get ONE
  `approval-needed` notification per marked commit (the hold is
  re-derived every tick; without the guard the bell would refill every
  interval). The manual/expedited button is unchanged and ignores the
  marker: it IS the dispatch path for these changes.
- **Clearing**: nothing to reset. Once a manual run delivers the marked
  commit, the pins stand past it and the next ordinary commit flows
  normally.
- **Image-time keys never brake**, even when the catalog marks them
  high-risk: `secureboot.*` and `diskUnlock.*` are written into the
  image and stay inert until a device is re-imaged (design 0001), so
  rolling them out changes nothing on a running fleet. `diskUnlock`
  options carrying `riskClass: high` would otherwise hold every save
  they appear in for no gain.

## Consequences

- Updates board reads as status, not controls: "All devices current" /
  "Release N flowing: infra done, fleet in 2h".
- Core updates (DAWO-NixOS bumps) inherit the same flow after their
  change request merges - one mental model for every change class.
- The stale-pin class of bugs (ex-ring groups keeping ancient pins)
  matters less: an idle engine keeps flowing the fleet to HEAD.
