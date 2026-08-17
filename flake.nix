{
  description = "Sextant - declarative fleet control-plane for NixOS";

  inputs.nixpkgs.url = "github:NixOS/nixpkgs/nixos-unstable";

  outputs = { self, nixpkgs }:
    let
      systems = [ "x86_64-linux" "aarch64-linux" ];

      # Go 1.26.6, ahead of nixpkgs, because seven called advisories are fixed
      # there and nowhere earlier: GO-2026-5026, 5972, 6088, 6089, 6090, 6091
      # and 6218. They reach net/http, net/url, html/template, crypto/tls and
      # encoding/asn1, all of which a web console calls, so excepting them in
      # .govulncheck-exceptions would have been risk acceptance rather than a
      # fix. nixpkgs unstable carried 1.26.4 when they landed and 1.26.5 after
      # the input bump in this commit; neither is enough.
      #
      # REMOVE THIS the moment nixpkgs ships 1.26.6 or later. A local version
      # override is a small fork of the toolchain: it stops receiving whatever
      # nixpkgs does to its go derivation, and the longer it stays the more of
      # that it silently misses. `nix flake update nixpkgs` then `nix develop
      # --command go version` is the whole check.
      goVersion = "1.26.6";
      goOverlay = _final: prev: {
        go = prev.go.overrideAttrs (_old: {
          version = goVersion;
          src = prev.fetchurl {
            url = "https://go.dev/dl/go${goVersion}.src.tar.gz";
            hash = "sha256-oHIcVMaIkBRI13rZs+x+p8R0cwdV/4kTgukuy5P/LLE=";
          };
        });
      };

      forAll = f: nixpkgs.lib.genAttrs systems
        (system: f (import nixpkgs { inherit system; overlays = [ goOverlay ]; }));
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
        # Per-class export: an overlay passes one evaluated host per device
        # class and every entry is tagged with the classes whose image
        # defines it. This is what makes CatalogEntry.AppliesTo and the
        # generator's class filtering able to keep their promise.
        exportCatalogFromClassOptions =
          (import ./nix/export-catalog.nix { lib = nixpkgs.lib; }).exportCatalogFromClassOptions;
        tests = import ./nix/tests.nix { lib = nixpkgs.lib; };
      };

      packages = forAll (pkgs: rec {
        sextant = pkgs.buildGoModule {
          pname = "sextant";
          version = "0.1.0";
          # Only the Go source tree feeds the build - filtered rather than
          # `src = self` (the whole flake tree). Unfiltered, the source
          # derivation's hash depended on every unrelated file in the repo
          # (docs, chart, nix fixtures), so touching any of them busted the
          # Go build cache with no Go source change, and any accidentally
          # committed non-Go file would ride along into the store path.
          src = pkgs.lib.fileset.toSource {
            root = ./.;
            fileset = pkgs.lib.fileset.unions [
              ./go.mod
              ./go.sum
              ./vendor
              ./cmd
              ./internal
            ];
          };
          # Dependencies are vendored; no network during build.
          vendorHash = null;
          subPackages = [ "cmd/sextant" "cmd/sxctl" ];
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

        # An SBOM and a CVE report for a fleet's closures (audit finding S3).
        # It lives here rather than in an overlay because every fleet needs
        # it and none of them should carry its own copy: an overlay runs
        # `nix run sextant#fleet-sbom -- . out` against its own flake.
        #
        # sbomnix and vulnxscan are deliberately NOT in runtimeInputs, and it
        # is not because they are awkward to package (they are: sbomnix does
        # not build against this nixpkgs pin, whose pandas is newer than one
        # of its dependencies accepts). Pinning a vulnerability scanner is
        # wrong on its own terms. A scanner and its feed age badly, and one
        # frozen in this lock would quietly stop seeing everything published
        # after the day we last bumped it - the exact failure a CVE report is
        # supposed to prevent. The caller supplies them, so the answer comes
        # from today's tooling:
        #
        #   nix shell github:tiiuae/sbomnix -c \
        #     nix run .#fleet-sbom -- . out
        #
        # The script checks for them on PATH and says so plainly if absent.
        fleet-sbom = pkgs.writeShellApplication {
          name = "fleet-sbom";
          runtimeInputs = with pkgs; [ jq nix coreutils findutils ];
          text = builtins.readFile ./scripts/fleet-sbom.sh;
          meta = {
            description = "SBOM and vulnerability report for a fleet's NixOS closures";
            homepage = "https://code.overheid.nl/MinBZK/DAWO-Sextant";
            mainProgram = "fleet-sbom";
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
            tailwindcss # `just css` rebuilds the console stylesheet
            jq # catalog drift guard canonicalises the export (regen-catalog.sh)
            govulncheck # vulnerability gate runs on THIS pinned toolchain (scripts/vulncheck.sh)
            # Rust toolchain for the device agent (agent/): the README and
            # `just ci` run cargo fmt/clippy/test, so the shell must carry them.
            cargo
            rustc
            clippy
            rustfmt
            gcc # `cc` for crate build scripts (proc-macro2, ring)
          ];
        };
      });

      # Device-side agent module (ADR 0010).
      nixosModules.agent = import ./deploy/nixos/agent.nix { inherit self; };

      # Root action executor: consumes the agent's intent spool to lock or
      # crypto-wipe a device (design 0004). Wipe is gated and default-off.
      nixosModules.actd = import ./deploy/nixos/actd.nix;

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
