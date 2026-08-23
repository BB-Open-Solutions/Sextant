# What the NixOS module would actually run, checked without building a system.
#
# The VM test next to this one boots a real machine and is the better proof,
# but it pulls a whole NixOS closure - on a machine without a warm cache that
# is hours of compiling, so nobody runs it and it proves nothing on the days
# it matters. This one evaluates the module and reads the unit back. It costs
# a second.
#
# It exists because the module used to pass only --addr: the service could not
# be given a repository or the write path at all, and nothing noticed.
{ self, pkgs, nixpkgs }:
let
  mk = cfg: (nixpkgs.lib.nixosSystem {
    system = pkgs.stdenv.hostPlatform.system;
    modules = [
      self.nixosModules.default
      ({ ... }: {
        boot.loader.grub.enable = false;
        fileSystems."/" = { device = "/dev/null"; fsType = "ext4"; };
        system.stateVersion = "24.05";
        services.sextant = cfg;
      })
    ];
  }).config.systemd.services.sextant.serviceConfig;

  full = mk {
    enable = true;
    repo = "/srv/overlay";
    write = true;
    gate = "remote";
    gateUrl = "https://gate.example.org";
  };
  inState = mk {
    enable = true;
    repo = "/var/lib/sextant/overlay";
    gate = "eval";
  };
in
pkgs.runCommand "sextant-console-module-check"
{
  execStart = full.ExecStart;
  rwPaths = builtins.concatStringsSep "," full.ReadWritePaths;
  rwInState = builtins.concatStringsSep "," inState.ReadWritePaths;
  inStateExec = inState.ExecStart;
} ''
  fail() { echo "FAIL: $1" >&2; exit 1; }

  # Every option that shapes the deployment has to reach the command line.
  for flag in "--addr 127.0.0.1:8080" "--gate remote" "--repo /srv/overlay" \
              "--gate-url https://gate.example.org" "--write" "--secure-cookies"; do
    case "$execStart" in
      *"$flag"*) ;;
      *) fail "ExecStart is missing $flag: $execStart" ;;
    esac
  done

  # ProtectSystem=strict makes only the state directory writable, so a repo
  # outside it has to be named - and one inside it must NOT be, or the unit
  # carries a path it already owns.
  [ "$rwPaths" = "/srv/overlay" ] || fail "ReadWritePaths for an outside repo = '$rwPaths'"
  [ -z "$rwInState" ] || fail "a repo inside the state dir was added to ReadWritePaths: '$rwInState'"

  # A read-only console must not be handed the write path by accident.
  case "$inStateExec" in
    *--write*) fail "write appeared without being asked for: $inStateExec" ;;
  esac

  touch $out
''
