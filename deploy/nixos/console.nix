# The Sextant console as a systemd service.
#
# The container path without the container: one host carries the binary, you
# bring the database and the TLS. See
# docs/handbook/src/operators/deployment-paths.md for how the three compare.
#
# WHAT THIS MODULE LEARNED THE HARD WAY. It used to pass only --addr, so the
# service could not be given a repository or the write path at all - and
# --write is a flag with no environment equivalent, so an EnvironmentFile
# could not supply it either. The result was a console that started, looked
# healthy and could change nothing. Every setting that shapes the deployment
# is an option here now, and `extraArgs` is the escape hatch for the rest.
{ self }:
{ config, lib, pkgs, ... }:
let
  cfg = config.services.sextant;
  # The repository is the config plane: the service commits to it, so it has
  # to be writable by the unit. With ProtectSystem=strict the only writable
  # place is the state directory, which is why a repo outside it has to be
  # named explicitly in ReadWritePaths.
  stateDir = "/var/lib/sextant";
  needsRW = cfg.repo != null && !(lib.hasPrefix stateDir cfg.repo);
  # DynamicUser is the right default for a read-only console: no account to
  # manage, no uid to leak. It is the wrong default the moment the console has
  # to WRITE a repository the operator prepared, because the uid does not
  # exist until the unit starts - so nothing can be chowned to it in advance,
  # and StateDirectory lives under /var/lib/private where root's own git
  # refuses to work ("dubious ownership"). Measured in the VM test on
  # 2026-08-23, which is what this option exists for.
  dynamic = cfg.user == null;
in
{
  options.services.sextant = {
    enable = lib.mkEnableOption "Sextant fleet control-plane";

    package = lib.mkOption {
      type = lib.types.package;
      default = self.packages.${pkgs.system}.sextant;
      description = "Sextant package to run.";
    };

    addr = lib.mkOption {
      type = lib.types.str;
      default = "127.0.0.1:8080";
      description = "HTTP listen address (put a TLS proxy in front).";
    };

    user = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      example = "sextant";
      description = ''
        Run as this user instead of a DynamicUser, and create it. Set it when
        the console writes a repository you prepared yourself: a DynamicUser's
        uid does not exist until the unit starts, so there is nothing to give
        the repository to beforehand.

        Left unset the service runs as a DynamicUser, which is the better
        default for a read-only console.
      '';
    };

    repo = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      example = "/var/lib/sextant/overlay";
      description = ''
        The overlay working tree: the fleet's configuration, which this
        service reads and commits to. Without it the console runs with no
        config plane - it starts and can change nothing.

        A path outside ${stateDir} is added to ReadWritePaths automatically;
        it still has to exist and be owned by the unit's user.
      '';
    };

    write = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Enable the write path: mutations and commits. Off by default because
        a read-only console is the safe thing to start with, and because a
        write path with no validation gate is refused rather than assumed.
      '';
    };

    gate = lib.mkOption {
      type = lib.types.enum [ "eval" "remote" "none" ];
      default = "eval";
      description = ''
        The validation gate. `eval` runs nix in-process on this host;
        `remote` delegates to a gate-runner; `none` skips validation and,
        together with write, has to be acknowledged with allowUnvalidated.
      '';
    };

    gateUrl = lib.mkOption {
      type = lib.types.nullOr lib.types.str;
      default = null;
      description = "Base URL of the gate-runner, when gate = \"remote\".";
    };

    allowUnvalidated = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = ''
        Acknowledge running the write path with gate = "none". The server
        refuses that combination otherwise, deliberately: it commits config
        to devices without proving it builds.
      '';
    };

    secureCookies = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = ''
        Mark session cookies Secure. Required behind TLS on a non-loopback
        address - the server refuses to ship session cookies without it.
      '';
    };

    environmentFile = lib.mkOption {
      type = lib.types.nullOr lib.types.path;
      default = null;
      description = ''
        EnvironmentFile with the secrets (SEXTANT_PG_DSN, SEXTANT_SECRET_KEY,
        SEXTANT_SESSION_KEY, SEXTANT_OIDC_CLIENT_SECRET, ...), e.g. an
        agenix-rendered file. Secrets never go on the command line.
      '';
    };

    extraArgs = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ ];
      example = [ "--org-name" "Zaanstad" ];
      description = "Extra arguments, for anything this module does not name.";
    };
  };

  config = lib.mkIf cfg.enable {
    assertions = [
      {
        assertion = cfg.gate != "remote" || cfg.gateUrl != null;
        message = "services.sextant.gate = \"remote\" needs services.sextant.gateUrl.";
      }
      {
        # The same refusal the server makes, made at build time instead: a
        # host that would not start is better caught by nixos-rebuild than by
        # a crash loop with the reason three levels deep in a log.
        assertion = !(cfg.write && cfg.gate == "none") || cfg.allowUnvalidated;
        message = ''
          services.sextant.write with gate = "none" commits unvalidated config
          to devices. Use gate = "remote" (recommended) or "eval", or set
          services.sextant.allowUnvalidated = true for a deliberate dev cell.
        '';
      }
      {
        assertion = !cfg.write || cfg.repo != null;
        message = "services.sextant.write needs services.sextant.repo: there is nothing to commit to.";
      }
    ];

    users.users = lib.mkIf (!dynamic) {
      ${cfg.user} = {
        isSystemUser = true;
        group = cfg.user;
        description = "Sextant fleet control-plane";
      };
    };
    users.groups = lib.mkIf (!dynamic) { ${cfg.user} = { }; };

    # The repository has to exist and be the service's before it starts: the
    # console does not clone it, it expects a working tree.
    systemd.tmpfiles.rules = lib.optional (!dynamic && cfg.repo != null)
      "d ${toString cfg.repo} 0750 ${cfg.user} ${cfg.user} -";

    systemd.services.sextant = {
      description = "Sextant fleet control-plane";
      wantedBy = [ "multi-user.target" ];
      after = [ "network-online.target" ];
      wants = [ "network-online.target" ];
      # git for the config plane; nix only when this host runs the gate
      # itself. A remote gate keeps nix off the console host on purpose.
      path = [ pkgs.git ] ++ lib.optional (cfg.gate == "eval") pkgs.nix;
      serviceConfig = {
        ExecStart = lib.escapeShellArgs ([
          (lib.getExe cfg.package)
          "--addr" cfg.addr
          "--gate" cfg.gate
        ]
        ++ lib.optionals (cfg.repo != null) [ "--repo" (toString cfg.repo) ]
        ++ lib.optionals (cfg.gateUrl != null) [ "--gate-url" cfg.gateUrl ]
        ++ lib.optional cfg.write "--write"
        ++ lib.optional cfg.allowUnvalidated "--allow-unvalidated"
        ++ lib.optional cfg.secureCookies "--secure-cookies"
        ++ cfg.extraArgs);
        EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
        DynamicUser = dynamic;
        User = lib.mkIf (!dynamic) cfg.user;
        Group = lib.mkIf (!dynamic) cfg.user;
        StateDirectory = "sextant";
        WorkingDirectory = stateDir;
        ReadWritePaths = lib.optional needsRW (toString cfg.repo);
        # Graceful shutdown: SIGTERM, then give in-flight writes time.
        KillSignal = "SIGTERM";
        TimeoutStopSec = 30;
        Restart = "on-failure";
        RestartSec = 2;
        # Hardening.
        NoNewPrivileges = true;
        ProtectSystem = "strict";
        ProtectHome = true;
        PrivateTmp = true;
        PrivateDevices = true;
        ProtectKernelTunables = true;
        ProtectKernelModules = true;
        ProtectControlGroups = true;
        RestrictAddressFamilies = [ "AF_INET" "AF_INET6" "AF_UNIX" ];
        RestrictNamespaces = true;
        LockPersonality = true;
        MemoryDenyWriteExecute = true;
        SystemCallArchitectures = "native";
        CapabilityBoundingSet = "";
      };
    };
  };
}
