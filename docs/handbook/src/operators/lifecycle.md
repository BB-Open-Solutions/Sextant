# Update, retire and wipe

## Update

Updates ship as a **rollout**: a new revision promotes through ordered waves,
each gated on health and a soak window, with an optional manual test gate.
Day to day this is driven from the **Updates** board - see
[Ship an update](./updates.md) for the full walkthrough and
[How a rollout ships](../concepts/rollout.md) for the concept behind it.

## Provisional: a device that exists but has not spoken

Enrolling a device creates its record immediately, in the **provisional** state.
The first successful check-in promotes it to active.

That state exists because an install can fail, and a record left behind by a
failed attempt used to break more than itself: a ring made up entirely of
devices that never arrived could not converge by definition, so the whole
rollout waited for machines that were never coming.

So a provisional device is counted differently. It is a real record - you can
see it, name it, and re-image the same chassis onto it rather than minting a
second one - but it does not hold a ring back, because it has never claimed to
be running anything.

**Abandoned enrolments are listed rather than deleted.** Somebody starts an
installation that never reports: unfamiliar hardware, a slow link, a station
operator called away, a laptop enrolled on Friday that does not boot until
Monday. The console surfaces those as a list for an operator to act on, because
the two mistakes are not symmetric - reaping too early deletes a record somebody
is still using, reaping too late leaves a stale row in a list.

## Retire

Retiring a device keeps its record for audit but stops image builds, check-ins
and rollout counting. Reactivation is an explicit, audited step.

## Lock and wipe

Lock and wipe are **intent-as-data**: the console records the intent as an
audited change (on a device's own page, under the red-bordered remote-actions
panel); the device pulls it on check-in and acts locally. There is no live
command channel.

- **Lock** locks all sessions and persists across reboot; clear the intent to
  release.
- **Wipe** cryptographically erases the device by destroying its LUKS key slots.
  It is irreversible and gated: the root executor refuses a wipe unless the
  device is locked first, unless the intent is explicitly sent with force
  (which the console's wipe action does, backed instead by a
  type-the-device-tag confirmation as the human safety net). Arm a device for
  wipe only when it is cleared to be wiped.

## Troubleshooting

**A wipe shows "refused" on the device page or in
[Compliance](./compliance.md).**
The device declined the intent - typically because a local interlock blocked
it even with force set. Clear the intent, confirm the device is locked, then
re-send the wipe.

**A wipe shows "failed".**
The device attempted the crypto-wipe but never confirmed completion back to
the console. Treat the disk as *not yet confirmed destroyed* - verify by
other means before reusing or disposing of the hardware.

**Retiring a device does not remove it from the fleet count.**
That is by design - retiring keeps the audit record and simply stops new
image builds, check-ins and rollout counting. Use *Remove* instead if the
device should be unenrolled entirely; unlike a retire, removal cannot be
undone by reactivation.
