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
  #
  # options is the evaluating host's own option tree, and a setting whose
  # option this IMAGE does not declare is skipped rather than applied.
  #
  # WHY. Scopes reach across device classes: an organisation setting covers
  # laptops and stations alike, and the station's image has no `dawo.apps`.
  # Applying it anyway fails that host's evaluation, which fails the whole
  # change - so the console refuses an edit the operator had every reason to
  # expect to work, and says only that an option does not exist. That is what
  # happened on 2026-08-03 with apps.comms.enable at org scope.
  #
  # Asking the option tree rather than catalog.json on purpose: the image is
  # ground truth and cannot drift from itself. The catalog's class tags exist
  # for the CONSOLE, so a person can see which classes a setting reaches
  # before they save it; this is the safety net underneath that.
  #
  # Only existence is read, never a value, so there is no recursion between
  # what we set and what we check.
  settingsModule = { options, catalogKeys ? null }: fleet: tag:
    let
      resolved = resolver.resolve fleet tag;
      declared = key: lib.hasAttrByPath ([ "dawo" ] ++ splitKey key) options;
      # A key this image does not declare is one of two very different
      # things, and conflating them is how a typo would sail through:
      #   - known to the catalog, absent from THIS class's image: skip it,
      #     which is the whole point of the exercise;
      #   - known to no image at all: a mistake, and the gate is the last
      #     line of defence for a fleet.json edited outside the console.
      # Without catalogKeys the generator cannot tell them apart, so it keeps
      # the old behaviour and applies everything - loosening silently would be
      # worse than the problem being fixed.
      keep = key: _:
        if declared key then true
        # No catalog to consult: apply it and let the module system reject an
        # option that does not exist. That is the pre-2026-08 behaviour, and
        # skipping instead would quietly swallow typos.
        else if catalogKeys == null then true
        else if lib.elem key catalogKeys then false
        else throw "device ${tag}: setting '${key}' is not an option in any image (typo?)";
      one = key: r:
        lib.setAttrByPath ([ "dawo" ] ++ splitKey key)
          (if r.enforced then lib.mkForce r.value else lib.mkDefault r.value);
    in
    lib.foldl' lib.recursiveUpdate { }
      (lib.mapAttrsToList one (lib.filterAttrs keep resolved));

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

  # ringBranchFor: the first ring (plan order) whose group covers the device
  # AND into which the device has been released decides the branch; no such
  # ring means main. A wave is either uncapped (maxDevices 0/absent: the whole
  # group is released) or count-capped (ADR 0013: a device is released only
  # when its pin equals the ring group, which the rollout engine sets cohort by
  # cohort). So an unreleased device in a capped wave's group stays on main
  # until the engine widens the cohort to include it.
  ringBranchFor = fleet: tag:
    let
      dev = fleet.devices.${tag} or { };
      rings = (fleet.rollout or { }).rings or [ ];
      # Reuse the resolver's ancestry walk - it carries the cycle guard, so a
      # cyclic groups.*.parent chain truncates instead of overflowing the stack.
      covered = lib.unique
        (lib.concatMap (g: resolver.groupAncestry fleet g) (dev.groups or [ ]));
      matching = lib.filter (r: lib.elem r.group covered) rings;
      released = lib.filter
        (r: ((r.maxDevices or 0) == 0) || ((dev.pin or "") == r.group))
        matching;
    in
    if released == [ ] then "main" else "rings/${(lib.head released).group}";

  # mkModules: the pure module list for one device - testable with
  # lib.evalModules, no nixosSystem required.
  mkModules =
    { fleet
    , tag
    , overlaysDir ? null # dir with <name>.nix for repo-defined overlays
      # catalogKeys: every setting key any image declares (the names in
      # catalog.json). Lets the generator tell a setting this class does not
      # have from one that exists nowhere. Null keeps the pre-2026-08 rule:
      # apply everything, and let an unknown option fail the evaluation.
    , catalogKeys ? null
    }:
    let
      apps = appLists fleet tag;
      overlayModules = map
        (name:
          let path = "${toString overlaysDir}/${name}.nix"; in
          # Overlay names are lookups, never paths: reject anything but a plain
          # slug so a value like "../secret/payload" from fleet.json cannot
          # escape overlaysDir and pull an arbitrary .nix file into the build.
          if builtins.match "[A-Za-z0-9_-]+" name == null
          then throw "device ${tag}: invalid overlay name '${name}' (only letters, digits, - and _)"
          else if overlaysDir == null
          then throw "device ${tag} selects overlay '${name}' but the generator got no overlaysDir"
          else if !builtins.pathExists path
          then throw "device ${tag} selects overlay '${name}' but ${path} does not exist"
          else path)
        apps.overlays;
    in
    [
      bridgeModule
      ({ pkgs, options, ... }: {
        config = lib.recursiveUpdate (settingsModule { inherit options catalogKeys; } fleet tag) {
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
      # coreModules is the default host recipe (the module set every device
      # gets before its per-host settings). coreModulesFor overrides it per
      # host, so one fleet can carry more than one IMAGE - a desktop laptop
      # recipe and a headless server/station recipe - chosen by the device's
      # class. Default: every host gets coreModules.
    , coreModules
    , coreModulesFor ? (_: coreModules)
    , hardwareProfiles
    , overlaysDir ? null
    , extraModules ? [ ]
      # specialArgsFor: tag -> specialArgs for that host. Cores that expect
      # flake inputs or a per-host identity (DAWO's hostConfig) get them
      # here; the generator itself never depends on them.
    , specialArgsFor ? (_: { })
      # extraModulesFor: tag -> extra modules for THAT host only. This is how
      # an infra host (an imaging station: PXE, harmonia, the imaging runner)
      # carries its role modules while staying a normal fleet device -
      # unlike extraModules, which every host gets. A plain workplace device
      # returns [].
    , extraModulesFor ? (_: [ ])
      # catalogKeys: see mkModules. An overlay passes
      # `map (e: e.name) (builtins.fromJSON (builtins.readFile ./catalog.json))`.
    , catalogKeys ? null
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
          modules = (coreModulesFor tag)
            ++ [ hardwareProfiles.${fleet.devices.${tag}.hardware} ]
            ++ mkModules { inherit fleet tag overlaysDir catalogKeys; }
            ++ [{ networking.hostName = lib.mkDefault tag; }]
            ++ extraModules
            ++ extraModulesFor tag;
        });
}
