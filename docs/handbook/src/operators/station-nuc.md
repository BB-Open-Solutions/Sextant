# Set up an imaging station (NUC)

An imaging station (the "inspoelstraat") is a small always-on box that boots
devices over the network and reports what it sees to the console. You set one up
a few times and then use it continuously.

Fresh hardware installs with a USB NixOS installer and `nixos-anywhere`: the USB
brings the box up with SSH, and `nixos-anywhere` drives disko + install over SSH
from your workstation. A fresh box (Secure Boot off, no TPM key enrolled) uses
the `-install` variant: systemd-boot + a LUKS passphrase.

## 1. Write a NixOS minimal USB installer

```
lsblk                              # find the whole stick, e.g. /dev/sdb (NOT sdb1)
sudo dd if=<nixos-minimal>.iso of=/dev/sdX bs=4M status=progress oflag=sync
```

## 2. Boot the NUC and bring up SSH

Plug the USB and wired ethernet. Power on; the NUC boot menu is **F10** if it
does not auto-boot the stick. At the installer prompt:

```
sudo systemctl start sshd
sudo mkdir -p /root/.ssh
sudo tee /root/.ssh/authorized_keys < keys/id_ed25519.pub   # operator pubkey
ip -brief a                                                  # note the NUC's IP
```

## 3. Install

Pick a LUKS passphrase and store it in your password manager, then:

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

`--generate-hardware-config` re-probes the NUC and writes its real kernel
modules before building. Disko targets `/dev/nvme0n1` (the NUC standard).

## 4. Make it a Sextant station

1. In the console, under **Imaging stations** (owner), register a station and
   mint its report credential (shown once).
2. Put the credential on the station (the runtime token file the station agent
   reads). It then reports discovered devices to the console.
3. To image devices from it, wire the station runner to your fleet overlay - see
   [Image a device from the console](./image-devices.md).

Later: enrol Secure Boot (`-sb` variant), then enrol TPM2 on the device, then
run the steady-state `dawo-inspoelstraat` configuration.
