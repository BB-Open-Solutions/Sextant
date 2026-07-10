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
      };
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
}
