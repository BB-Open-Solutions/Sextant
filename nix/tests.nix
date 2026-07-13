# tests.nix - eval-only assertions over the generator and catalog export,
# run by the Go harness (generator_test.go) via `nix eval .#lib.tests`.
# Every attribute must evaluate to true; the harness fails on any false.
{ lib }:
let
  generator = import ./generator.nix { inherit lib; };
  catalog = import ./export-catalog.nix { inherit lib; };

  # A miniature "core": the option surface a real DAWO core would declare.
  core = {
    options.dawo = {
      secureboot = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Enforce Secure Boot with machine-owned keys.";
      };
      desktop = lib.mkOption {
        type = lib.types.str;
        default = "kde";
        description = "Desktop environment for this device.";
      };
      apps.office = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Office suite (LibreOffice).";
      } // { riskClass = "high"; };
      internal.plumbing = lib.mkOption {
        type = lib.types.int;
        default = 1;
        # no description: must NOT appear in the catalog
      };
    };
    # Swallow generator outputs a real NixOS would define elsewhere.
    options.environment.systemPackages = lib.mkOption {
      type = lib.types.listOf lib.types.anything;
      default = [ ];
    };
    options.networking.hostName = lib.mkOption {
      type = lib.types.str;
      default = "";
    };
  };

  # A site module that sets values NORMALLY - the piece enforced settings
  # must beat and default settings must lose to.
  site = {
    config.dawo.desktop = "gnome";
    config.dawo.secureboot = false;
  };

  fleet = {
    version = 3;
    org = {
      settings = { "apps.office" = true; };
      packages = [ "hello" ];
    };
    groups = {
      zaanstad = { settings.secureboot = true; enforced = [ "secureboot" ]; };
      frontoffice = { parent = "zaanstad"; };
    };
    devices.lt-1 = {
      groups = [ "frontoffice" ];
      hardware = "hw";
      settings.desktop = "plasma";
    };
    devices.lt-parked = {
      groups = [ "frontoffice" ];
      hardware = "hw";
      state = "retired";
    };
    policies.vpnpol = { settings = { "apps.office" = false; }; };
    filters.laptops = {
      rules = [{ attr = "tag"; op = "eq"; value = "lt-1"; }];
    };
    assignments = [
      { policy = "vpnpol"; target = "org"; filter = "laptops"; priority = 1; }
    ];
  };

  fakePkgs = { hello = "pkg:hello"; };

  eval = lib.evalModules {
    modules = [ core site ]
      ++ generator.mkModules { inherit fleet; tag = "lt-1"; };
    specialArgs = { pkgs = fakePkgs; };
  };
  cfg = eval.config;

  entries = catalog.exportCatalog { modules = [ core ]; };
  names = map (e: e.name) entries;
in
{
  # Enforced (group, mkForce) beats the site module's normal set.
  enforcedBeatsSite = cfg.dawo.secureboot == true;
  # Default-resolved (device, mkDefault) loses to the site module.
  defaultLosesToSite = cfg.dawo.desktop == "gnome";
  # Policy default (org) loses to inline org setting per tie-break
  # (inline beats policy at equal specificity).
  inlineBeatsPolicy = cfg.dawo.apps.office == true;
  # Additive packages resolve against pkgs by attribute path.
  packagesResolved = cfg.environment.systemPackages == [ "pkg:hello" ];
  # The bridge carries the device tag.
  tagBridged = cfg.sextant.deviceTag == "lt-1";
  # Catalog: documented options exported with dotted names and types...
  catalogHasSecureboot = lib.elem "secureboot" names;
  catalogHasNestedApp = lib.elem "apps.office" names;
  catalogTypeIsBool =
    (lib.head (lib.filter (e: e.name == "secureboot") entries)).type == "boolean";
  # ...and undocumented options stay out.
  catalogHidesUndocumented = !(lib.elem "internal.plumbing" names);
  # Defaults export when JSON-representable...
  catalogCarriesDefault =
    (lib.head (lib.filter (e: e.name == "desktop") entries)).default == "kde";
  # ...and the riskClass annotation (mkOption // { riskClass = ...; })
  # travels into the entry; unannotated options carry none.
  catalogCarriesRiskClass =
    (lib.head (lib.filter (e: e.name == "apps.office") entries)).riskClass == "high";
  catalogOmitsRiskClassByDefault =
    !((lib.head (lib.filter (e: e.name == "secureboot") entries)) ? riskClass);
  # Update funnel (ADR 0011): a device inside a ring subtree follows its
  # ring branch; the ring covers children via ancestry.
  cominBranchFollowsRing =
    let
      ringFleet = fleet // {
        rollout.rings = [{ group = "zaanstad"; }];
      };
      ev = lib.evalModules {
        modules = [ core site ] ++ generator.mkModules {
          fleet = ringFleet;
          tag = "lt-1"; # member of frontoffice, child of zaanstad
        };
        specialArgs = { pkgs = fakePkgs; };
      };
    in
    ev.config.sextant.cominBranch == "rings/zaanstad";
  # ...and a device outside every ring stays on main.
  cominBranchDefaultsToMain = cfg.sextant.cominBranch == "main";

  # Cohort pinning (ADR 0013): in a COUNT-CAPPED wave an unreleased device
  # (no pin) stays on main even though its group is the ring - it has not been
  # released into the cohort yet.
  cappedRingUnreleasedStaysMain =
    let
      ringFleet = fleet // {
        rollout.rings = [{ group = "zaanstad"; maxDevices = 1; }];
      };
      ev = lib.evalModules {
        modules = [ core site ] ++ generator.mkModules {
          fleet = ringFleet;
          tag = "lt-1";
        };
        specialArgs = { pkgs = fakePkgs; };
      };
    in
    ev.config.sextant.cominBranch == "main";
  # ...and once the engine releases it (pin == the ring group) it follows the
  # ring branch, just like an uncapped wave.
  cappedRingReleasedFollowsRing =
    let
      ringFleet = fleet // {
        rollout.rings = [{ group = "zaanstad"; maxDevices = 1; }];
        devices = fleet.devices // {
          lt-1 = fleet.devices.lt-1 // { pin = "zaanstad"; };
        };
      };
      ev = lib.evalModules {
        modules = [ core site ] ++ generator.mkModules {
          fleet = ringFleet;
          tag = "lt-1";
        };
        specialArgs = { pkgs = fakePkgs; };
      };
    in
    ev.config.sextant.cominBranch == "rings/zaanstad";

  # A cyclic groups.*.parent chain must not overflow the generator: ancestry
  # is walked through the resolver's cycle-guarded helper, so it degrades to
  # "main" instead of crashing the build.
  cyclicGroupsDoNotOverflow =
    let
      cyc = fleet // {
        groups = { a = { parent = "b"; }; b = { parent = "a"; }; };
        devices = { lt-1 = { hardware = "hw"; groups = [ "a" ]; }; };
        rollout = { rings = [ ]; };
        policies = { };
        filters = { };
      };
      ev = lib.evalModules {
        modules = [ core site ] ++ generator.mkModules { fleet = cyc; tag = "lt-1"; };
        specialArgs = { pkgs = fakePkgs; };
      };
    in
    ev.config.sextant.cominBranch == "main";

  # An overlay name is a lookup, never a path: a traversal like "../evil"
  # from fleet.json is rejected rather than pulling in an arbitrary .nix file.
  overlayNameRejectsTraversal =
    let
      cfg = fleet // {
        devices = { lt-1 = { hardware = "hw"; groups = [ ]; overlays = [ "../evil" ]; }; };
        policies = { };
        filters = { };
      };
      r = builtins.tryEval (builtins.deepSeq
        (generator.mkModules { fleet = cfg; tag = "lt-1"; overlaysDir = ./.; }) true);
    in
    !r.success;

  # Lifecycle: a retired device has no host attribute (attrNames is lazy,
  # so the dummy nixpkgs is never forced).
  retiredDeviceHasNoHost =
    let
      hosts = lib.attrNames (generator.mkFleet {
        nixpkgs = null;
        system = "x86_64-linux";
        inherit fleet;
        coreModules = [ core ];
        hardwareProfiles = { hw = { }; };
      });
    in
    hosts == [ "lt-1" ];
}
