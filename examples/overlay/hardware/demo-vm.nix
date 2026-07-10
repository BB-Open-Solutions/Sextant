# Minimal eval-able hardware profile: enough NixOS to force a toplevel
# derivation (what the gate does), no real machine assumptions.
{ lib, modulesPath, ... }:
{
  imports = [ "${modulesPath}/profiles/minimal.nix" ];
  boot.loader.grub.enable = false;
  fileSystems."/" = {
    device = "none";
    fsType = "tmpfs";
  };
  system.stateVersion = "25.11";
  nixpkgs.hostPlatform = lib.mkDefault "x86_64-linux";
}
