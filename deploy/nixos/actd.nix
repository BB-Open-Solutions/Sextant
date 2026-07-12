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
    runtimeInputs = [ pkgs.cryptsetup pkgs.util-linux pkgs.systemd pkgs.coreutils ];
    # The wipe gates (armWipe, allowUnlockedWipe, poweroffAfterWipe) are
    # resolved at build time into DIFFERENT emitted bash, rather than compared
    # as strings in the script - so an unarmed host ships a script that simply
    # cannot erase, and shellcheck sees no constant comparisons.
    text = ''
      set -euo pipefail
      spool="${spoolDir}"

      # lock: lock every session. Idempotent - safe to run each beat.
      if [ -e "$spool/lock.intent" ]; then
        echo "sextant-actd: lock intent - locking all sessions"
        loginctl lock-sessions || true
      fi

      # wipe: destructive, heavily gated.
      if [ -e "$spool/wipe.intent" ]; then
      ${if !cfg.armWipe then ''
        echo "sextant-actd: WIPE intent present but this host is NOT armed (services.sextant-actd.armWipe=false); refusing" >&2
      '' else ''
        ${lib.optionalString (!cfg.allowUnlockedWipe) ''
        if [ ! -e "${lockFlag}" ]; then
          echo "sextant-actd: WIPE refused - device is not locked first (lock interlock); set allowUnlockedWipe to override" >&2
          exit 0
        fi
        ''}
        echo "sextant-actd: WIPE armed and interlock satisfied - erasing LUKS key slots" >&2
        mapfile -t devs < <(blkid -t TYPE=crypto_LUKS -o device || true)
        if [ ''${#devs[@]} -eq 0 ]; then
          echo "sextant-actd: no LUKS devices found to erase" >&2
        fi
        for dev in "''${devs[@]}"; do
          echo "sextant-actd: luksErase $dev" >&2
          cryptsetup luksErase --batch-mode "$dev" || echo "sextant-actd: luksErase failed on $dev" >&2
        done
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
      serviceConfig = {
        Type = "oneshot";
        ExecStart = lib.getExe actd;
        # Root, but no more than needed: it locks sessions and runs cryptsetup.
        User = "root";
        ProtectHome = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        LockPersonality = true;
      };
    };
  };
}
