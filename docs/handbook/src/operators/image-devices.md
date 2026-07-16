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

Open the **provisioning wizard** (linked from the jobs table) for the guided,
per-device view of the same batch. It walks a four-phase stepper - **Install
-> Secure Boot -> TPM2 -> Done** - and adapts to what each device's hardware
profile actually needs:

- A device whose group needs no Secure Boot simply skips that phase and goes
  straight from installed to done.
- When a device reaches the Secure Boot phase, the wizard shows a
  brand-specific manual action (the firmware entry key and the exact BIOS
  steps - e.g. Lenovo/ThinkPad enters on **F1**, Intel NUC on **F2**, HP on
  **F10**) and an in-console **reboot** control, since only a human at the
  keyboard can toggle Secure Boot in firmware.
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
in the BIOS (Setup Mode -> Enabled -> Save & Exit) before rebooting; a reboot
without that toggle just returns the device to the same phase.

**The device converges but its posture still shows Secure Boot/TPM2 as not
enforcing.**
Give it one more check-in cycle - posture is self-reported by the device on
check-in, so it lags the wizard's own phase tracking by up to one interval.
