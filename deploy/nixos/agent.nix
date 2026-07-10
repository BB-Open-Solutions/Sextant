# NixOS module for the Sextant device agent (ADR 0010). Import from the
# Sextant flake and enable per device:
#
#   imports = [ sextant.nixosModules.agent ];
#   services.sextant-agent = {
#     enable = true;
#     tag = "lt-00042";
#     url = "https://console.bb-open.com";
#   };
#
# The per-device credential is runtime state (never in the nix store):
# write it to credentialFile once at provisioning (shown at enrollment).
{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.sextant-agent;
in
{
  options.services.sextant-agent = {
    enable = lib.mkEnableOption "Sextant device agent";
    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.system}.sextant-agent;
      description = "Agent package to run.";
    };
    url = lib.mkOption {
      type = lib.types.str;
      description = "Sextant console base URL.";
    };
    tag = lib.mkOption {
      type = lib.types.str;
      description = "Device asset tag as enrolled in the fleet.";
    };
    credentialFile = lib.mkOption {
      type = lib.types.str;
      default = "/var/lib/sextant-agent/credential";
      description = "Runtime file with the per-device credential (0600, root).";
    };
    interval = lib.mkOption {
      type = lib.types.ints.positive;
      default = 60;
      description = "Seconds between check-ins.";
    };
    facter = lib.mkOption {
      type = lib.types.nullOr lib.types.package;
      default = pkgs.nixos-facter or null;
      description = "nixos-facter package for hardware facts; null disables.";
    };
  };

  config = lib.mkIf cfg.enable {
    systemd.services.sextant-agent = {
      description = "Sextant device agent";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      serviceConfig = {
        ExecStart = lib.getExe cfg.package;
        Environment = [
          "SEXTANT_URL=${cfg.url}"
          "SEXTANT_TAG=${cfg.tag}"
          "SEXTANT_INTERVAL=${toString cfg.interval}"
        ] ++ lib.optional (cfg.facter != null)
          "SEXTANT_FACTER=${lib.getExe cfg.facter}";
        # The credential reaches the agent as a systemd credential: the
        # file stays root-owned, the DynamicUser process gets a private
        # read-only copy.
        LoadCredential = "credential:${cfg.credentialFile}";
        DynamicUser = true;
        Restart = "on-failure";
        RestartSec = 10;
        # Exit 3 = retired: permanent, a restart loop would hammer the
        # console with 410s forever.
        RestartPreventExitStatus = 3;
        # Hardening: the agent only reads /run/current-system, runs
        # facter and talks HTTPS.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" "AF_NETLINK" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        SystemCallArchitectures = "native";
        CapabilityBoundingSet = "";
      };
    };
  };
}
