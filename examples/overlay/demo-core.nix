# demo-core.nix - a miniature stand-in for the DAWO-NixOS core: the
# documented dawo.* option surface the catalog exports and the generator
# targets. A real overlay imports the DAWO core modules here instead.
{ config, lib, pkgs, ... }:
{
  options.dawo = {
    desktop = lib.mkOption {
      type = lib.types.enum [ "plasma" "gnome" ];
      default = "plasma";
      description = "Desktop environment for this device.";
    };
    secureboot = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enforce Secure Boot with machine-owned keys.";
    };
    apps.office = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Office suite (LibreOffice).";
    };
    apps.media = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Media playback (VLC).";
    };
    ssh.maxAuthTries = lib.mkOption {
      type = lib.types.ints.positive;
      default = 3;
      description = "Maximum SSH authentication attempts per connection.";
    };
  };

  config = {
    # Demo effects: enough that the options are real, harmless to eval.
    services.openssh.settings.MaxAuthTries = config.dawo.ssh.maxAuthTries;
    environment.systemPackages =
      lib.optional config.dawo.apps.office pkgs.hello
      ++ lib.optional config.dawo.apps.media pkgs.hello;

    # Flatpaks assigned as data arrive on the bridge (consume when the
    # real core wires services.flatpak).
    warnings = lib.optional
      (config.sextant.flatpaks != [ ] && !config.services.flatpak.enable or false)
      "flatpaks assigned but flatpak service not enabled in the demo core";
  };
}
