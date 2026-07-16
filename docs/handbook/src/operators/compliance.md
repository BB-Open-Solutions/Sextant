# Track compliance

**Compliance** answers one question: which devices are not to spec right now,
and why. It is the full drill-down behind the compliance donut on
**Overview** - the donut caps its attention queue at 8 items; this page lists
every open incident.

## Reading the page

The three summary chips (**All**, **Critical**, **Warning**) double as a
filter - click one to narrow the device table below it. Every active
(non-retired) device appears, worst status first:

- **Critical** - an error the device reported, or a wipe that failed or was
  refused.
- **Warning** - offline, never checked in, or behind its target revision.
- **To spec** - no open incident.

Each row lists the device's issues with a short **title**, a **detail** (the
specifics - e.g. which revision it is running versus its target, or when it
was last seen), and a suggested **action**.

## What raises an incident

| Kind | Severity | Raised when |
|---|---|---|
| Never seen | Warning | Enrolled but no check-in has ever arrived |
| Offline | Warning | Stopped checking in within the online window |
| Behind | Warning | Online, but running a different revision than its group's target |
| Errored | Critical | The device reported a build/apply error on check-in |
| Wipe refused | Warning | The device declined a wipe intent (unarmed, or an interlock - typically "not locked first" - blocked it) |
| Wipe failed | Critical | A crypto-wipe was attempted but did not confirm completion |

A single device can carry several incidents at once (e.g. offline *and*
behind). Suggested actions point at the fix: verify imaging and connectivity
for never-seen, check power/network for offline, check the rollout and
device logs for behind, open the device to inspect the failure for errored,
and re-arm or clear the interlock for a refused wipe (see
[Update, retire and wipe](./lifecycle.md)).

## Policy exposure

Below the device table, a per-policy table shows where each policy is
assigned (its scope targets) and, of the devices under those targets, how
many currently carry an open incident - a revision-level proxy for "this
policy may not actually be applied everywhere it is assigned yet". A policy
with no assignments shows as unassigned; it has no effect until targeted.

## Troubleshooting

**A device shows as behind right after a rollout started.**
Expected - "behind" just means the device has not yet pulled and converged
to its target revision. Give it a check-in cycle or two before treating it
as stuck; if it stays behind well past the wave's soak window, check
[Ship an update](./updates.md) for whether the wave itself is stalled.

**A device never clears "never seen".**
It has an enrolled record but has not reported at all. Confirm it was
actually imaged (see [Image a device from the console](./image-devices.md))
and that its agent credential and network reach the console - the same
credential/newline pitfall documented in
[Set up an imaging station](./station-nuc.md) applies to a device's own
agent credential too.

**A wipe keeps showing "refused".**
The root executor requires the device to be locked first (or the intent to
be forced) before it will act on a wipe. Re-arm the wipe intent, or lock the
device first, then retry - see
[Update, retire and wipe](./lifecycle.md).
