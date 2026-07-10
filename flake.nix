{
  description = "Sextant - declarative fleet control-plane for NixOS";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];
      forAll = f: nixpkgs.lib.genAttrs systems (system: f nixpkgs.legacyPackages.${system});
    in
    {
      # The resolver/generator contract (ADR 0005): overlays import these
      # from this flake so console and generator share one source.
      lib = {
        resolve = import ./nix/resolve.nix { };
        generator = import ./nix/generator.nix { lib = nixpkgs.lib; };
        exportCatalog =
          (import ./nix/export-catalog.nix { lib = nixpkgs.lib; }).exportCatalog;
        exportCatalogFromOptions =
          (import ./nix/export-catalog.nix { lib = nixpkgs.lib; }).exportCatalogFromOptions;
        tests = import ./nix/tests.nix { lib = nixpkgs.lib; };
      };

      packages = forAll (pkgs: rec {
        sextant = pkgs.buildGoModule {
          pname = "sextant";
          version = "0.1.0";
          src = self;
          # Dependencies are vendored; no network during build.
          vendorHash = null;
          subPackages = [ "cmd/sextant" "cmd/dfctl" ];
          env.CGO_ENABLED = "0";
          ldflags = [ "-s" "-w" ];
          meta = {
            description = "Declarative fleet control-plane for NixOS";
            homepage = "https://code.overheid.nl/MinBZK/DAWO-Sextant";
            mainProgram = "sextant";
          };
        };
        default = sextant;

        # The device agent (ADR 0010): Rust, static-friendly, observes and
        # reports only - comin does the converging.
        sextant-agent = pkgs.rustPlatform.buildRustPackage {
          pname = "sextant-agent";
          version = "0.1.0";
          src = ./agent;
          cargoLock.lockFile = ./agent/Cargo.lock;
          meta = {
            description = "Sextant device agent: check-in and hardware facts";
            homepage = "https://code.overheid.nl/MinBZK/DAWO-Sextant";
            mainProgram = "sextant-agent";
          };
        };
      });

      devShells = forAll (pkgs: {
        default = pkgs.mkShell {
          packages = with pkgs; [
            go
            golangci-lint
            just
            git
            postgresql # psql client for local dev against the compose database
          ];
        };
      });

      # Device-side agent module (ADR 0010).
      nixosModules.agent = import ./deploy/nixos/agent.nix { inherit self; };

      nixosModules.default = { config, lib, pkgs, ... }:
        let cfg = config.services.sextant;
        in {
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
            environmentFile = lib.mkOption {
              type = lib.types.nullOr lib.types.path;
              default = null;
              description = ''
                EnvironmentFile with secrets (SEXTANT_* variables), e.g. an
                agenix-rendered file. Secrets never go on the command line.
              '';
            };
          };

          config = lib.mkIf cfg.enable {
            systemd.services.sextant = {
              description = "Sextant fleet control-plane";
              wantedBy = [ "multi-user.target" ];
              after = [ "network-online.target" ];
              wants = [ "network-online.target" ];
              path = [ pkgs.git pkgs.nix ];
              serviceConfig = {
                ExecStart = "${lib.getExe cfg.package} --addr ${cfg.addr}";
                EnvironmentFile = lib.mkIf (cfg.environmentFile != null) cfg.environmentFile;
                DynamicUser = true;
                StateDirectory = "sextant";
                WorkingDirectory = "/var/lib/sextant";
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
        };
    };
}
