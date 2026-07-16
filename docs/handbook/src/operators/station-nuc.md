# Set up an imaging station (NUC)

An imaging station (the "inspoelstraat") is a small always-on box that boots
target devices over PXE and reports what it finds to the console. From there
you dispatch imaging jobs at the target devices - the station itself is set up
once (and occasionally re-provisioned), then left running.

This chapter is the full procedure, kale NUC to working station. Steps marked
**Manual step** are not driven by the console today - they are candidates for
future turnkey automation, but for now someone does them by hand at a
keyboard or a workstation shell.

## Checklist

Work through these in order. The station is done when the last box is ticked.

- [ ] **Manual step** - write a NixOS minimal USB installer
- [ ] **Manual step** - boot the NUC from the USB, bring up SSH
- [ ] **Manual step** - run `nixos-anywhere` from your workstation to install
      the `-install` variant (systemd-boot + LUKS passphrase, Secure Boot off)
- [ ] Register the station in the console and mint its report credential
      (owner, console step)
- [ ] **Manual step** - put the credential on the station and point the agent
      at the console
- [ ] Verify the station shows as registered and reachable in the console
- [ ] **Manual step** - PXE-boot a test target device on the station's network
      and confirm it appears as discovered
- [ ] Wire the station to your fleet overlay so it can dispatch real imaging
      jobs (see [Image a device from the console](./image-devices.md))
- [ ] *(Later, once the fleet needs it)* **Manual step** - re-provision the
      station on the `-sb` variant (Secure Boot on) and enrol TPM2, then move
      it to the steady-state `dawo-inspoelstraat` configuration

## 1. Write a NixOS minimal USB installer — manual step

```
lsblk                              # find the whole stick, e.g. /dev/sdb (NOT sdb1)
sudo dd if=<nixos-minimal>.iso of=/dev/sdX bs=4M status=progress oflag=sync
```

Double-check the device node against `lsblk` before running `dd` - the whole
disk, not a partition, and never the workstation's own disk.

## 2. Boot the NUC and bring up SSH — manual step

Plug in the USB and wired ethernet. Power on; the NUC's boot menu is **F10**
if it does not auto-boot the stick (some models use **F2** or **Esc** -
check the splash screen). At the installer prompt:

```
sudo systemctl start sshd
sudo mkdir -p /root/.ssh
sudo tee /root/.ssh/authorized_keys < keys/id_ed25519.pub   # operator pubkey
ip -brief a                                                  # note the NUC's IP
```

## 3. Install with nixos-anywhere — manual step

Pick a LUKS passphrase and store it in your password manager first, then
drive the install from your workstation (not the NUC itself):

```
cd inspoelstraat-appliance
printf %s '<LUKS-passphrase>' > /tmp/luks.key
NIX_SSHOPTS="-o IdentitiesOnly=yes -o StrictHostKeyChecking=no" \
  nix run github:nix-community/nixos-anywhere -- \
    --flake .#dawo-inspoelstraat-install \
    --target-host root@<installer-ip> \
    -i ../keys/id_ed25519 \
    --generate-hardware-config nixos-generate-config ./hardware-configuration.nix \
    --phases disko,install \
    --disk-encryption-keys /tmp/luks.key /tmp/luks.key
rm -f /tmp/luks.key
```

Notes:

- `--generate-hardware-config` re-probes the NUC and writes its real kernel
  modules before building - do not reuse a hardware config from a different
  box.
- Disko targets `/dev/nvme0n1` (the NUC standard). Adjust the disko module if
  your hardware differs.
- `printf %s` (not `echo`) writes the passphrase with **no trailing
  newline**. This matters again in step 4 - see the troubleshooting note
  below.
- This installs the `-install` variant: Secure Boot off, no TPM2 key
  enrolled, systemd-boot + a LUKS passphrase at boot. That is deliberate -
  Secure Boot and TPM2 are enrolled later, once the box is confirmed working.

The NUC reboots into its fresh, minimal NixOS install once this completes.

## 4. Register the station in the console

In the console, under **Organisation -> Imaging stations** (owner reach):

1. **Register an imaging station**: give it a tag (e.g. `dawo-inspoelstraat`,
   matching `[a-z0-9][a-z0-9-]*`), an optional description and site.
2. **Mint credential**: the console generates a bearer token and shows the
   **report endpoint** (`https://<console-host>/api/station/<tag>/report`)
   and the token together. This is a one-shot reveal - the token is never
   shown again, so copy both before leaving the page.

## 5. Put the credential on the station — manual step

Configure the station's report-agent (in the `inspoelstraat-appliance` flake)
with the endpoint and bearer token from step 4, so it can `POST` its
discovery reports to the console. Exactly how the agent picks up the token is
a station-flake concern (a runtime credential file is the usual pattern, same
shape as the per-device agent credential - see
[Install and configure Sextant](./deploy.md)); the important operational rule
is the same one that trips up the per-device credential too:

> **Write the token with no trailing newline.** `printf %s '<token>' > path`
> or a paste that strips the newline both work; `echo '<token>' > path` (or a
> plain heredoc) leaves a `\n` at the end of the file. A bearer token with a
> stray newline never matches what the console minted, and the station's
> reports fail authentication with no more detail than "unauthorized" - see
> Troubleshooting below.

## 6. Verify the station is live

Back in **Organisation -> Imaging stations**, open the station. If the agent
is configured correctly it appears registered and (once it has reported at
least once) shows a device count from its PXE network. If nothing shows up
yet, PXE-boot a target device on the station's network next - see step 7.

## 7. Confirm discovery — manual step (boot a target device)

PXE-boot any spare target device on the station's network. It should appear
under the station's discovered devices within a minute or two. This proves
the whole chain: agent -> report endpoint -> console -> discovered plane.

## 8. Wire the station to your fleet overlay

The station is now registered and reporting, but imaging real devices from it
needs the station's runner wired to your fleet overlay (so it can build and
install the right configuration for each target). See
[Image a device from the console](./image-devices.md) for the imaging flow
itself, driven from **Enrollment** in the console.

## Later: Secure Boot and TPM2

The steps above deliberately leave Secure Boot off and TPM2 unenrolled - get
a working station first. Once it is confirmed:

1. **Manual step** - re-provision with the `-sb` flake variant (Secure Boot
   enabled, still a LUKS passphrase at boot).
2. **Manual step** - enrol TPM2 on the device so the passphrase is no longer
   needed at boot (it remains as break-glass recovery - see
   [Manage secrets](./secrets.md)).
3. Move the station onto the steady-state `dawo-inspoelstraat` configuration.

This same Install -> Secure Boot -> TPM2 -> Done progression is what the
provisioning wizard walks an operator through per target device - see
[Image a device from the console](./image-devices.md).

## Troubleshooting

**The station never shows a device count / discovered devices.**
Check, in order: the station's network actually serves PXE to the target
device; the agent process is running on the station and can reach the
console's report endpoint (`curl -i <report-url>` from the station should at
least get a 401, not a connection failure); and the credential file has no
trailing newline (see step 5).

**Minting a new credential "loses" the station.**
It does not - re-minting only invalidates the *previous* token. Update the
credential file on the station with the new one (again, no trailing
newline) and it resumes reporting.

**`nixos-anywhere` fails partway through disko/install.**
Re-run it - `nixos-anywhere --phases disko,install` is safe to repeat against
a booted installer. If it fails consistently on the same step, boot the
installer again fresh (a half-partitioned disk can confuse a second disko
pass) before retrying.

**SSH from the workstation hangs or is refused.**
Confirm `ip -brief a` on the installer shows an address reachable from your
workstation (not just link-local), and that `sudo systemctl start sshd` was
run *after* the installer environment finished booting, not during POST.
