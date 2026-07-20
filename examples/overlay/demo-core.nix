# demo-core.nix - a miniature stand-in for the DAWO-NixOS core: the
# documented dawo.* option surface the catalog exports and the generator
# targets. A real overlay imports the DAWO core modules here instead.
{ config, lib, pkgs, ... }:
{
  options.dawo = {
    # label (appended like riskClass) is the human name the console leads
    # with; the dotted path stays as the technical identity.
    desktop = lib.mkOption {
      type = lib.types.enum [ "plasma" "gnome" ];
      default = "plasma";
      description = "Desktop environment for this device.";
    } // { label = "Desktop environment"; };
    # riskClass (appended after mkOption) surfaces as a warning badge in
    # the console settings editor.
    secureboot = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enforce Secure Boot with machine-owned keys.";
    } // { riskClass = "high"; label = "Secure Boot"; };
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
    } // { label = "SSH login attempts"; };
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
