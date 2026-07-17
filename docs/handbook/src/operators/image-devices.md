# Image a device from the console

Once a station is registered and reporting (see
[Set up an imaging station](./station-nuc.md)), imaging target devices is
driven entirely from the console's **Enrollment** page - no shell access to
the station is needed for a routine batch.

## Step 1: choose the station

Open **Enrollment**, pick the station whose PXE network the target devices
are on, and continue. If no station is listed, none is registered yet - see
[Set up an imaging station](./station-nuc.md).

## Step 2: batch-dispatch the discovered devices

**Pre-flight (one firmware visit per device, before PXE):** enable the
Security Chip (TPM2) and clear it once; enable Secure Boot AND reset it to
setup mode (clears the factory keys). With that state set, everything after
Dispatch runs hands-off - signed install, key enrolment, TPM2 sealing,
verification - with no BIOS visit afterwards.

PXE-boot the target devices; they appear as discovered rows (MAC, vendor,
model, disk size) under the chosen station. Imaging is **batch-only** by
design - one audited pass images a whole rack rather than one device at a
time:

1. Set the shared **hardware profile** (suggested from make and model where
   the profile catalog matches), **class** and **group** for this rack of
   otherwise-identical machines.
2. Tick the devices to include, and give each one a CMDB name (the tag it
   will enrol under).
3. **Dispatch imaging**. This creates each device's record, captures its
   hardware specs, and queues an image job per device.

## Step 3: watch the jobs run

The imaging-jobs table shows each device's status live: pending -> imaging
(with a progress bar and current step) -> installed, or failed. Under the
hood, the station runner claims each job, resolves the target's IP from its
DHCP lease, runs `nixos-anywhere` against that device's generated
configuration, and bakes in the device's one-time agent credential.

The install stages everything the security ceremony will need, so nothing
has to be generated or typed on the device later:

- **Per-device Secure Boot signing keys**, generated on the station (`sbctl`)
  and shipped with the install. When the device's resolved config enables
  `secureboot.enable`, the boot chain is signed *during the install itself* -
  no separate "audit mode" deploy round-trip.
- **A disk-encryption recovery phrase** - six plain words (diceware), typed
  exactly as revealed, instead of a 32-character random string. It is sealed
  into the per-device secret store; revealing it is owner-only and audited.
- **A one-shot TPM2 enrol key**, staged root-only inside the encrypted
  volume; the on-device executor uses it once to seal the disk to the TPM2
  and then shreds it.

After the install the device reboots into its new system by itself.

Open the **provisioning wizard** (linked from the jobs table) for the guided,
per-device view of the same batch. It walks a four-phase stepper - **Install
-> Secure Boot -> TPM2 -> Done** - and adapts to what each device's hardware
profile actually needs:

- Which phases apply is decided by the device's **resolved config**
  (`secureboot.enable`, `diskUnlock.tpm2.enable`) and its **hardware** (no
  EFI -> no Secure Boot phase; no TPM2 chip -> no TPM2 phase). A device that
  needs neither goes straight from installed to done on its first check-in.
- Exactly **one manual step** remains: when a device reaches the Secure Boot
  phase, the wizard shows a brand-specific firmware action (the entry key
  and the exact BIOS steps - e.g. Lenovo/ThinkPad enters on **F1**, Intel
  NUC on **F2**, HP on **F10**) and an in-console **reboot** control. Enable
  Secure Boot and reset to setup mode; everything after that is automatic -
  the device enrols its (pre-staged) keys, reboots enforcing, seals the disk
  to the TPM2 and reboots once more.
- Every phase turns green only on what the device itself reports (firmware
  state, executor acknowledgements) - the final **done** card is a
  verification: Secure Boot observed enforcing, TPM2 sealing confirmed by
  the executor that performed it.
- Each row carries a plain-language "Now:" line telling a non-expert exactly
  what is happening or what to do; the page refreshes itself.
- The wizard also tells you when it is safe to unplug a device (once its
  phase reads **done** - before that, keep it cabled so it can keep checking
  in and converging).
- If a device produced a one-time LUKS recovery key during provisioning, the
  wizard shows it once (a break-glass secret store keeps it recoverable
  later for an organisation owner - see [Manage secrets](./secrets.md)).

## Step 4: converge

Each imaged device boots, checks in, and converges its configuration. It then
shows on the device page with its facts and posture (Secure Boot / TPM2
state), and on [Compliance](./compliance.md) if anything about it needs
attention.

## Troubleshooting

**A discovered device never gets an IP / the job stalls at "imaging".**
The runner resolves the target's IP from its DHCP lease; if the station's PXE
network hands out leases slowly or the device NICs are ambiguous (multiple
discovered rows with a similar MAC prefix), remove the stale discovered row
and re-PXE-boot the device to get a fresh lease and report.

**A job fails.**
The jobs table and the wizard both surface the failure message inline. Most
failures are hardware-profile mismatches (wrong disk device assumed by disko)
or a target that lost network mid-install - cancel the job, fix the profile
or cabling, and dispatch it again; a failed job does not block the rest of
the batch.

**The Secure Boot step never completes.**
Check the manual firmware action shown in the wizard was actually completed
in the BIOS (Enabled + Reset to Setup Mode -> Save & Exit) before rebooting; a
reboot without that toggle just returns the device to the same phase. The key
enrolment itself is automatic once the firmware is in setup mode.

**The device converges but its posture still shows Secure Boot/TPM2 as not
enforcing.**
Give it one more check-in cycle - posture is self-reported by the device on
check-in, so it lags the wizard's own phase tracking by up to one interval.
