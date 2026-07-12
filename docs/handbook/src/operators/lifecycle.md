# Update, retire and wipe

## Update

Updates ship as a **rollout**: a new revision promotes through ordered waves,
each gated on health and a soak window, with an optional manual test gate. See
[How a rollout ships](../concepts/rollout.md).

## Retire

Retiring a device keeps its record for audit but stops image builds, check-ins
and rollout counting. Reactivation is an explicit, audited step.

## Lock and wipe

Lock and wipe are **intent-as-data**: the console records the intent as an
audited change; the device pulls it on check-in and acts locally. There is no
live command channel.

- **Lock** locks all sessions and persists across reboot; clear the intent to
  release.
- **Wipe** cryptographically erases the device by destroying its LUKS key slots.
  It is irreversible and gated: the root executor refuses unless the device is
  explicitly armed (a per-device change) and locked first. Arm a device only
  when it is cleared to be wiped.
