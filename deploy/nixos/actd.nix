# NixOS module for the Sextant root action executor (sextant-actd, design 0004).
#
# The unprivileged agent (services.sextant-agent) can only DROP a validated
# intent file into a spool directory; it never holds the privilege to lock a
# session or destroy a disk. This root oneshot consumes that spool and carries
# out the action. Two intents are handled:
#
#   lock   - lock all logind sessions. Reversible (the operator clears the
#            intent; the agent stops re-spooling).
#   wipe   - cryptographically erase the device by destroying every LUKS
#            key slot (cryptsetup luksErase). IRREVERSIBLE.
#
# Wipe is gated deliberately, in layers, because it is destructive:
#   1. armWipe (default FALSE). A spooled wipe is REFUSED and logged loudly
#      unless this host explicitly arms it. Arming is a per-device Nix change
#      - the deliberate "go" for that one machine, reviewed like any config.
#   2. Lock interlock: the device must already be locked (the lock flag the
#      agent writes) before a wipe runs, unless allowUnlockedWipe is set.
#   3. The intent itself is an audited, operator-set fleet change that the
#      agent only relays; there is no live command channel.
#
# Import from the Sextant flake alongside the agent:
#   imports = [ sextant.nixosModules.agent sextant.nixosModules.actd ];
#   services.sextant-actd.enable = true;                # lock works; wipe stays refused
#   services.sextant-actd.armWipe = true;               # ONLY on a device cleared to wipe
{ config, lib, pkgs, ... }:
let
  cfg = config.services.sextant-actd;

  # Spool + lock-flag paths must match the agent (agent/src/action.rs).
  spoolDir = "/run/sextant-intent";
  lockFlag = "/var/lib/sextant-agent/locked";

  actd = pkgs.writeShellApplication {
    name = "sextant-actd";
    runtimeInputs = [
      pkgs.cryptsetup
      pkgs.util-linux
      pkgs.systemd
      pkgs.coreutils
      # The provision step: sbctl enrols the staged Secure Boot keys;
      # findutils/gawk/gnugrep read the EFI variables and crypttab. Listed
      # explicitly - coreutils does NOT provide find/awk/grep, and a missing
      # runtime input fails silently inside command substitutions (the
      # station-runner's awk lesson).
      pkgs.sbctl
      pkgs.findutils
      pkgs.gawk
      pkgs.gnugrep
    ];
    # The wipe gates (armWipe, allowUnlockedWipe, poweroffAfterWipe) are
    # resolved at build time into DIFFERENT emitted bash, rather than compared
    # as strings in the script - so an unarmed host ships a script that simply
    # cannot erase, and shellcheck sees no constant comparisons.
    text = ''
      set -euo pipefail
      spool="${spoolDir}"

      # writeAck records the OUTCOME for the unprivileged agent to read and
      # forward to the console on its next beat, so "delivered" (spooled) is
      # never confused with "executed"/"refused"/"failed" (design 0004). The
      # ack lives in persistent state (matching the agent's ACK_FILE), not
      # the tmpfs spool: provisioning steps reboot right after acking, and
      # the outcome must survive the reboot.
      writeAck() { printf '%s' "$1" > "/var/lib/sextant-agent/action.ack" 2>/dev/null || true; }

      # lock: lock every session. Idempotent. The marker is removed after
      # handling so the path unit does not re-trigger this oneshot in a loop
      # while the file lingers; the agent re-spools it next beat if the console
      # intent is still set.
      if [ -e "$spool/lock.intent" ]; then
        echo "sextant-actd: lock intent - locking all sessions"
        loginctl lock-sessions || true
        writeAck lock
        rm -f "$spool/lock.intent" || true
      fi

      # reboot: one-shot restart so an operator can reach the BIOS during
      # provisioning (Secure Boot / TPM2 firmware steps) without walking to the
      # machine. Non-destructive. Loop guard via the kernel boot_id: a stamp
      # written before the reboot carries the boot_id it happened in; after the
      # machine comes back the boot_id differs, so we know the reboot completed,
      # ack it, and do NOT reboot again even if the console intent (and thus the
      # re-spooled marker) is still set until the console clears it.
      rebootStamp="/var/lib/sextant-agent/reboot-boot-id"
      if [ -e "$spool/reboot.intent" ]; then
        bootid="$(cat /proc/sys/kernel/random/boot_id 2>/dev/null || echo unknown)"
        if [ -e "$rebootStamp" ] && [ "$(cat "$rebootStamp" 2>/dev/null)" != "$bootid" ]; then
          echo "sextant-actd: reboot completed (boot_id changed) - acking"
          writeAck rebooted
          rm -f "$rebootStamp" "$spool/reboot.intent" || true
        elif [ ! -e "$rebootStamp" ]; then
          echo "sextant-actd: reboot intent - restarting for BIOS access"
          mkdir -p "$(dirname "$rebootStamp")"
          printf '%s' "$bootid" > "$rebootStamp"
          rm -f "$spool/reboot.intent" || true
          systemctl reboot || true
        else
          # Stamp matches the current boot: we already initiated the reboot this
          # boot; drop the re-spooled marker and wait for the machine to go down.
          rm -f "$spool/reboot.intent" || true
        fi
      fi

      # provision: advance the Secure Boot / TPM2 ceremony (wizard, design
      # 0004). Constructive and guarded: each visit performs at most the one
      # step the machine is actually ready for, acks it, and reboots so the
      # firmware state the next step depends on is real. The install stages
      # the material this consumes: per-device sbctl keys at /var/lib/sbctl
      # (lanzaboote signed the boot chain with them at install time) and a
      # one-shot copy of the LUKS key at /var/lib/sextant-actd/luks-enroll.key
      # (systemd-cryptenroll needs an existing secret to authorise a new
      # keyslot), shredded after use.
      if [ -e "$spool/provision.intent" ]; then
        rm -f "$spool/provision.intent" || true
        # efibool reads the value byte of an EFI variable (4-byte attribute
        # prefix, then the boolean - the kernel's documented efivarfs layout).
        efibool() {
          local f
          f="$(find /sys/firmware/efi/efivars -maxdepth 1 -name "$1-*" 2>/dev/null | head -n1)"
          if [ -z "$f" ]; then echo ""; return; fi
          od -An -tu1 "$f" 2>/dev/null | awk '{v=$NF} END {print v}'
        }
        sb="$(efibool SecureBoot)"; setup="$(efibool SetupMode)"
        keyfile="/var/lib/sextant-actd/luks-enroll.key"
        if [ "$sb" != "1" ] && [ "$setup" = "1" ] && [ -d /var/lib/sbctl/keys ]; then
          # Firmware in setup mode with our signing keys present: enrol them
          # (plus Microsoft's certificates - option ROMs and shims stay
          # bootable). Secure Boot enforces from the next boot on.
          echo "sextant-actd: firmware in setup mode - enrolling Secure Boot keys"
          # --disable-landlock: sbctl's own sandbox has already bitten this
          # flow once (the station's key export); this unit's systemd
          # sandbox (ProtectSystem=strict + explicit ReadWritePaths) is the
          # containment here, and stacking the two risks a refused write to
          # a path systemd allows.
          if sbctl --disable-landlock enroll-keys --microsoft; then
            writeAck sb-enrolled
            systemctl reboot || true
          else
            echo "sextant-actd: sbctl enroll-keys failed" >&2
            writeAck sb-enroll-failed
          fi
        elif [ "$sb" = "1" ] && [ -e /dev/tpmrm0 ] && [ -e "$keyfile" ] \
            && grep -q "tpm2-device" /etc/crypttab 2>/dev/null; then
          # Secure Boot enforcing, a TPM2 present, the config wires a TPM2
          # unlock and the enrol key is still staged: seal the LUKS keyslot
          # to PCR 7 (the Secure Boot state). The passphrase keyslot stays
          # as break-glass. The staged key is shredded either way - it is
          # single-purpose material.
          echo "sextant-actd: Secure Boot enforcing - sealing LUKS to the TPM2 (PCR 7)"
          dev="$(blkid -t TYPE=crypto_LUKS -o device | head -n1)"
          if [ -n "$dev" ] && systemd-cryptenroll --unlock-key-file="$keyfile" \
               --tpm2-device=auto --tpm2-pcrs=7 "$dev"; then
            shred -u "$keyfile" 2>/dev/null || rm -f "$keyfile"
            writeAck tpm2-enrolled
            systemctl reboot || true
          else
            echo "sextant-actd: systemd-cryptenroll failed on ''${dev:-<no LUKS device>}" >&2
            shred -u "$keyfile" 2>/dev/null || rm -f "$keyfile"
            writeAck tpm2-enroll-failed
          fi
        fi
        # Otherwise: waiting on a firmware step (or nothing applies). No ack -
        # the console keeps showing the operator instruction; the agent
        # re-spools next beat while the intent stands.
      fi

      # wipe: destructive, heavily gated.
      if [ -e "$spool/wipe.intent" ]; then
      ${if !cfg.armWipe then ''
        echo "sextant-actd: WIPE intent present but this host is NOT armed (services.sextant-actd.armWipe=false); refusing" >&2
        writeAck wipe-refused
        # Clear the refused marker so the path unit does not respawn on a tight
        # loop; the agent re-spools it next beat if the intent is still armed.
        rm -f "$spool/wipe.intent" || true
      '' else ''
        ${lib.optionalString (!cfg.allowUnlockedWipe) ''
        if [ ! -e "${lockFlag}" ]; then
          echo "sextant-actd: WIPE refused - device is not locked first (lock interlock); set allowUnlockedWipe to override" >&2
          writeAck wipe-refused
          rm -f "$spool/wipe.intent" || true
          exit 0
        fi
        ''}
        echo "sextant-actd: WIPE armed and interlock satisfied - erasing LUKS key slots" >&2
        mapfile -t devs < <(blkid -t TYPE=crypto_LUKS -o device || true)
        if [ ''${#devs[@]} -eq 0 ]; then
          echo "sextant-actd: no LUKS devices found to erase" >&2
        fi
        failed=0
        for dev in "''${devs[@]}"; do
          echo "sextant-actd: luksErase $dev" >&2
          if ! cryptsetup luksErase --batch-mode "$dev"; then
            echo "sextant-actd: luksErase FAILED on $dev" >&2
            failed=1
          fi
        done
        # A partial erase is NOT a completed wipe: keep the intent for a
        # rate-limited retry, do not power off, and fail loudly so the operator
        # sees the device is not fully wiped. Only a fully successful erase
        # clears the marker (and powers off).
        if [ "$failed" -ne 0 ]; then
          echo "sextant-actd: WIPE INCOMPLETE - one or more slots not erased; leaving intent for retry" >&2
          writeAck wipe-failed
          exit 1
        fi
        writeAck wipe
        rm -f "$spool/wipe.intent" || true
        ${lib.optionalString cfg.poweroffAfterWipe ''
        echo "sextant-actd: wipe complete - powering off" >&2
        systemctl poweroff || true
        ''}
      ''}
      fi
    '';
  };
in
{
  options.services.sextant-actd = {
    enable = lib.mkEnableOption "Sextant root action executor (lock/wipe)";

    armWipe = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Allow this device to EXECUTE a crypto-wipe (destroy LUKS key slots) when
        a wipe intent is spooled. Default false: a wipe is refused and logged.
        Set true only on a specific device that is cleared to be wiped - this is
        the deliberate per-device "go".
      '';
    };

    allowUnlockedWipe = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Skip the lock interlock (normally a wipe requires the device to be locked first).";
    };

    poweroffAfterWipe = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Power the machine off after a successful wipe.";
    };
  };

  config = lib.mkIf cfg.enable {
    # A path unit triggers the executor whenever an intent file appears; the
    # oneshot runs as root (unlike the sandboxed agent).
    systemd.paths.sextant-actd = {
      wantedBy = [ "multi-user.target" ];
      pathConfig = {
        PathExistsGlob = "${spoolDir}/*.intent";
        Unit = "sextant-actd.service";
      };
    };

    systemd.services.sextant-actd = {
      description = "Sextant root action executor";
      # A wipe that keeps failing (busy device, I/O error) leaves its intent and
      # exits non-zero; bound the retries so a broken erase is rate-limited into
      # a visible failed state instead of respawning forever.
      startLimitIntervalSec = 300;
      startLimitBurst = 5;
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe actd;
        # Root - it locks sessions and runs cryptsetup - but no more than
        # that. The unprivileged agent and the console both go further
        # (ProtectSystem, network denial, PrivateTmp); this unit lagged
        # behind despite having the largest blast radius of the three (root,
        # cryptsetup luksErase, systemctl reboot/poweroff). Bring it to the
        # same floor, staying conservative on SystemCallFilter/DeviceAllow -
        # this codebase has already been burned once by an untested
        # over-restrictive sandbox silently breaking the wipe path (see the
        # agent unit's RuntimeDirectory/StateDirectory history), and wipe
        # correctness matters more here than a maximal lockdown that cannot
        # be validated against real hardware in this change.
        ProtectSystem = "strict";
        # sbctl (enroll-keys, GUID bookkeeping) writes /var/lib/sbctl; the
        # provision step consumes and shreds the staged enrol key under
        # /var/lib/sextant-actd.
        ReadWritePaths = [ spoolDir "/var/lib/sextant-agent" "/var/lib/sextant-actd" "/var/lib/sbctl" ];
        ProtectHome = true;
        # NOT ProtectKernelTunables: it mounts /sys read-only, and the
        # provision step must write EFI variables (sbctl enroll-keys). The
        # rest of the sandbox stands.
        ProtectKernelTunables = false;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictSUIDSGID = true;
        LockPersonality = true;
        PrivateTmp = true;
        # This executor only talks to logind/systemd over the D-Bus unix
        # socket and to local LUKS block devices; it never needs the
        # network, so deny it outright.
        RestrictAddressFamilies = [ "AF_UNIX" ];
        IPAddressDeny = [ "any" ];
        SystemCallArchitectures = "native";
      };
    };
  };
}
