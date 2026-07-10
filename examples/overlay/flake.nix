{
  # Reference organisation overlay: the shape every customer repo takes.
  # It consumes the Sextant flake for the generator/resolver contract and
  # a core for the dawo.* option surface. Here the core is demo-core.nix;
  # a real organisation replaces it with the DAWO-NixOS input and its
  # hardware profiles - nothing else changes.
  description = "Example Sextant organisation overlay (fleet.json v3)";

  inputs = {
    sextant.url = "path:../..";
    nixpkgs.follows = "sextant/nixpkgs";
  };

  outputs = { self, sextant, nixpkgs }:
    let
      lib = nixpkgs.lib;
      fleet = builtins.fromJSON (builtins.readFile ./fleet.json);
      coreModules = [ ./demo-core.nix ];
      hardwareProfiles = {
        demo-vm = ./hardware/demo-vm.nix;
      };
    in
    {
      # The generator: one NixOS host per enrolled device. The console's
      # eval gate forces these hosts' toplevel derivations.
      nixosConfigurations = sextant.lib.generator.mkFleet {
        inherit nixpkgs fleet coreModules hardwareProfiles;
        system = "x86_64-linux";
        overlaysDir = ./overlays;
      };

      # The catalog (ADR 0005): documented dawo.* options, rendered by the
      # console. Regenerate with: nix eval .#catalog --json > catalog.json
      catalog = sextant.lib.exportCatalog { modules = coreModules; };
    };
}
