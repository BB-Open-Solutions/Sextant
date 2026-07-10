# generator.nix - the v3 fleet generator: fleet.json (v3) in, one NixOS
# host per device out. Settings resolve through nix/resolve.nix (the twin
# the parity harness proves equal to the console), then land as dawo.*
# option values: enforced -> mkForce (governance), default -> mkDefault
# (flexibility). Apps are additive data: packages resolve to nixpkgs
# attribute paths, overlays import repo-defined modules by name, flatpaks
# surface on the sextant.flatpaks bridge option for the core to consume.
#
# The generator never evaluates data as code: setting keys become option
# PATHS (unknown ones fail module evaluation - the gate), package and
# overlay names are lookups, never expressions.
{ lib }:
let
  resolver = import ./resolve.nix { };

  splitKey = key: lib.splitString "." key;

  # settingsModule: resolved settings -> dawo.* option values with the
  # right priority. Built as a nested attrset merged per key.
  settingsModule = fleet: tag:
    let
      resolved = resolver.resolve fleet tag;
      one = key: r:
        lib.setAttrByPath ([ "dawo" ] ++ splitKey key)
          (if r.enforced then lib.mkForce r.value else lib.mkDefault r.value);
    in
    lib.foldl' lib.recursiveUpdate { }
      (lib.mapAttrsToList one resolved);

  # additive app lists (apps.go): org + group ancestry + device, unioned.
  appLists = fleet: tag:
    let
      dev = fleet.devices.${tag} or { };
      anc = lib.unique
        (lib.concatMap (g: resolver.groupAncestry fleet g) (dev.groups or [ ]));
      scopes = [ (fleet.org or { }) ]
        ++ map (g: fleet.groups.${g} or { }) anc
        ++ [ dev ];
      union = field: lib.unique (lib.concatMap (s: s.${field} or [ ]) scopes);
    in
    {
      packages = union "packages";
      flatpaks = union "flatpaks";
      overlays = union "overlays";
    };

  # bridge: options the generator sets and the core/overlay consumes.
  bridgeModule = {
    options.sextant = {
      flatpaks = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        description = "Flathub app ids assigned to this device (additive across the scope chain).";
      };
      deviceTag = lib.mkOption {
        type = lib.types.str;
        description = "The fleet asset tag of this device.";
      };
      cominBranch = lib.mkOption {
        type = lib.types.str;
        default = "main";
        description = ''
          The overlay branch this device converges from. Ring devices
          follow their machine-owned rings/<group> branch, which the
          rollout engine promotes (ADR 0011); everything else follows
          main. The core wires this into services.comin.
        '';
      };
    };
  };

  # ringBranchFor: the first ring (plan order) whose group covers the
  # device via its group ancestry decides the branch; no ring means main.
  ringBranchFor = fleet: tag:
    let
      dev = fleet.devices.${tag} or { };
      rings = (fleet.rollout or { }).rings or [ ];
      ancestry = g:
        let parent = (fleet.groups.${g} or { }).parent or ""; in
        [ g ] ++ lib.optionals (parent != "") (ancestry parent);
      covered = lib.concatMap ancestry (dev.groups or [ ]);
      matching = lib.filter (r: lib.elem r.group covered) rings;
    in
    if matching == [ ] then "main" else "rings/${(lib.head matching).group}";

  # mkModules: the pure module list for one device - testable with
  # lib.evalModules, no nixosSystem required.
  mkModules =
    { fleet
    , tag
    , overlaysDir ? null # dir with <name>.nix for repo-defined overlays
    }:
    let
      apps = appLists fleet tag;
      overlayModules = map
        (name:
          let path = "${toString overlaysDir}/${name}.nix"; in
          if overlaysDir == null
          then throw "device ${tag} selects overlay '${name}' but the generator got no overlaysDir"
          else if !builtins.pathExists path
          then throw "device ${tag} selects overlay '${name}' but ${path} does not exist"
          else path)
        apps.overlays;
    in
    [
      bridgeModule
      ({ pkgs, ... }: {
        config = lib.recursiveUpdate (settingsModule fleet tag) {
          sextant.deviceTag = tag;
          sextant.cominBranch = ringBranchFor fleet tag;
          sextant.flatpaks = apps.flatpaks;
          environment.systemPackages = map
            (name:
              lib.attrByPath (splitKey name)
                (throw "device ${tag}: package '${name}' does not exist in nixpkgs")
                pkgs)
            apps.packages;
        };
      })
    ] ++ overlayModules;

  # deviceAsserts: clear generator-level errors before module evaluation.
  deviceAsserts = { fleet, tag, hardwareProfiles }:
    let dev = fleet.devices.${tag}; in
    lib.throwIfNot (fleet.version or 0 == 3)
      "fleet.json version ${toString (fleet.version or 0)}: this generator supports version 3"
      (lib.throwIfNot (hardwareProfiles ? ${dev.hardware or ""})
        "device ${tag}: unknown hardware profile '${dev.hardware or ""}'"
        (lib.throwIfNot
          (lib.all (g: fleet.groups or { } ? ${g}) (dev.groups or [ ]))
          "device ${tag}: references an unknown group"
          true));
in
{
  inherit mkModules settingsModule appLists;

  # mkFleet: nixosConfigurations for every device in the fleet.
  #   nixpkgs          the nixpkgs flake input (for lib.nixosSystem)
  #   system           e.g. "x86_64-linux"
  #   fleet            parsed fleet.json (builtins.fromJSON)
  #   coreModules      the DAWO core module list (dawo.* options)
  #   hardwareProfiles attrset name -> hardware module
  #   overlaysDir      optional dir with <name>.nix overlay modules
  #   extraModules     appended to every host
  mkFleet =
    { nixpkgs
    , system
    , fleet
    , coreModules
    , hardwareProfiles
    , overlaysDir ? null
    , extraModules ? [ ]
      # specialArgsFor: tag -> specialArgs for that host. Cores that expect
      # flake inputs or a per-host identity (DAWO's hostConfig) get them
      # here; the generator itself never depends on them.
    , specialArgsFor ? (_: { })
    }:
    # Retired devices keep their audit record in fleet.json but no longer
    # exist as hosts: no image builds, no gate target.
    lib.genAttrs
      (lib.attrNames
        (lib.filterAttrs (_: d: (d.state or "") != "retired")
          (fleet.devices or { })))
      (tag:
        assert deviceAsserts { inherit fleet tag hardwareProfiles; };
        nixpkgs.lib.nixosSystem {
          inherit system;
          specialArgs = specialArgsFor tag;
          modules = coreModules
            ++ [ hardwareProfiles.${fleet.devices.${tag}.hardware} ]
            ++ mkModules { inherit fleet tag overlaysDir; }
            ++ [{ networking.hostName = lib.mkDefault tag; }]
            ++ extraModules;
        });
}
