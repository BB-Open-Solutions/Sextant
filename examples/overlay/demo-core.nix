# demo-core.nix - a reference workstation baseline.
#
# This is the third layer of a Sextant deployment: the OS opinions a fleet
# runs. Sextant itself has none - it edits configuration, proves it builds and
# rolls it out; WHAT a device should be is this file's business. A real
# organisation replaces it with its own core (the DAWO core, or its own) and
# nothing else in the overlay changes.
#
# It exists to be forked. The goal is a laptop somebody can be handed: sound
# works in a call, the clock is right, firmware can be updated, a dock and a
# headset work, the disk is encrypted, and the security posture is stated
# rather than assumed. Add your own applications and you are done.
#
# It is NOT a certified baseline and does not claim to be one. It is a
# defensible starting point with its reasoning written down, which is more
# useful than a list of settings nobody can argue with because nobody knows
# why they are there.
#
# Two rules worth keeping when you extend it:
#
#   * Every option carries a description. The console renders the catalog from
#     these, so an option without one is invisible to the person who has to
#     operate it (ADR 0005).
#   * Controls come in TIERS. Mandatory-and-invisible is on by default;
#     anything that can lock a user out of their own machine is opt-in and says
#     why. A control that gets switched off in a panic protects nobody.
{ config, lib, pkgs, ... }:
let
  cfg = config.dawo;
in
{
  options.dawo = {
    desktop = lib.mkOption {
      type = lib.types.enum [ "gnome" "plasma" "none" ];
      default = "gnome";
      description = "Desktop environment. `none` gives a console-only machine, which is what a server or a kiosk wants.";
    } // { label = "Desktop environment"; };

    # Posture is image-time: changing it does nothing to a running device until
    # it is re-imaged, and the console says so next to these keys.
    secureboot = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Enforce Secure Boot with machine-owned keys. Takes effect when a device is (re)imaged: the firmware has to be in setup mode for the keys to be enrolled, which cannot be arranged remotely.";
    } // { riskClass = "high"; label = "Secure Boot"; };

    diskEncryption = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Encrypt the system disk (LUKS). On by default because a laptop leaves the building. Turning it off affects devices imaged afterwards - an encrypted disk does not become plain because a setting changed.";
    } // { riskClass = "high"; label = "Disk encryption"; };

    audio = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Sound and microphone (PipeWire), including the real-time scheduling a call needs. Off is for machines with nobody sitting at them.";
    } // { label = "Audio"; };

    bluetooth = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Bluetooth for headsets, mice and keyboards.";
    } // { label = "Bluetooth"; };

    docks = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Thunderbolt dock support (bolt). A dock is the first thing an office user plugs in; without this it is authorised by nobody and works for nobody.";
    } // { label = "Docking stations"; };

    firmwareUpdates = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Let devices fetch firmware updates (fwupd). BIOS and dock firmware are part of the attack surface, and a fleet that cannot be patched is a finding.";
    } // { label = "Firmware updates"; };

    printing = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Printing (CUPS), discovering printers announced on the local network. Off by default: it starts a daemon and makes the device answer for printers on whatever network it is on, which is an organisation's decision rather than a default.";
    } // { label = "Printing"; };

    # A wrong clock breaks single sign-on and makes every log useless for
    # correlation - which is exactly when you need them. Not optional.
    timeZone = lib.mkOption {
      type = lib.types.str;
      default = "Europe/Amsterdam";
      description = "System time zone.";
    } // { label = "Time zone"; };

    timeServers = lib.mkOption {
      type = lib.types.listOf lib.types.str;
      default = [ "0.pool.ntp.org" "1.pool.ntp.org" ];
      description = "NTP servers. Point these at your own infrastructure if you have it: a fleet that trusts a public pool for time is trusting it for authentication too.";
    } // { label = "Time servers"; };

    ssh.maxAuthTries = lib.mkOption {
      type = lib.types.ints.positive;
      default = 3;
      description = "Maximum SSH authentication attempts per connection.";
    } // { label = "SSH login attempts"; };

    usbDevices = {
      enable = lib.mkOption {
        type = lib.types.bool;
        default = false;
        description = "Block USB devices plugged in AFTER boot; whatever is present at boot keeps working. Opt-in and high risk: a device whose allowlist misses the keyboard cannot be recovered remotely. Set the allowlist first.";
      } // { riskClass = "high"; label = "USB device control"; };
      allowlist = lib.mkOption {
        type = lib.types.listOf lib.types.str;
        default = [ ];
        example = [ ''allow id 0bf8:1028 name "docking-keyboard"'' ];
        description = "USBGuard rules for devices that must work when plugged in later - a fixed dongle, a card reader, a dock's keyboard. Find the identifiers with `lsusb`.";
      } // { label = "USB allowed devices"; };
    };

    apps.office = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Office suite (LibreOffice).";
    } // { label = "Office suite"; };

    apps.browser = lib.mkOption {
      type = lib.types.bool;
      default = true;
      description = "Web browser (Firefox).";
    } // { label = "Web browser"; };
  };

  config = lib.mkMerge [
    {
      time.timeZone = cfg.timeZone;
      networking.timeServers = cfg.timeServers;
      # chrony rather than timesyncd: it copes with a laptop that suspends and
      # wakes on a different network, which timesyncd handles poorly.
      services.chrony.enable = true;

      services.openssh.settings.MaxAuthTries = cfg.ssh.maxAuthTries;

      # Power management for a machine on a battery. Deliberately NOT tlp: it
      # fights power-profiles-daemon, which is what the desktop's own power
      # controls talk to. Pick one, and pick the one the user can see.
      powerManagement.enable = true;
      services.power-profiles-daemon.enable = true;

      environment.systemPackages =
        lib.optional cfg.apps.office pkgs.libreoffice
        ++ lib.optional cfg.apps.browser pkgs.firefox;

      warnings = lib.optional
        (config.sextant.flatpaks != [ ] && !(config.services.flatpak.enable or false))
        "flatpaks assigned but the flatpak service is not enabled in this core";
    }

    (lib.mkIf (cfg.desktop == "gnome") {
      services.xserver.enable = true;
      services.displayManager.gdm.enable = true;
      services.desktopManager.gnome.enable = true;
    })

    (lib.mkIf (cfg.desktop == "plasma") {
      services.xserver.enable = true;
      services.displayManager.sddm.enable = true;
      services.desktopManager.plasma6.enable = true;
    })

    (lib.mkIf cfg.audio {
      services.pipewire = {
        enable = true;
        alsa.enable = true;
        pulse.enable = true;
      };
      # Without rtkit PipeWire cannot get the scheduling priority it needs and
      # audio stutters under load - which is during a call.
      security.rtkit.enable = true;
    })

    (lib.mkIf cfg.bluetooth { hardware.bluetooth.enable = true; })
    (lib.mkIf cfg.docks { services.hardware.bolt.enable = true; })
    (lib.mkIf cfg.firmwareUpdates { services.fwupd.enable = true; })

    (lib.mkIf cfg.printing {
      services.printing.enable = true;
      # Discovery needs mDNS in NSS, not just an avahi daemon: otherwise a
      # printer announces itself and nothing can resolve the name it announced.
      services.avahi = {
        enable = true;
        nssmdns4 = true;
        openFirewall = true;
      };
    })

    (lib.mkIf cfg.usbDevices.enable {
      services.usbguard = {
        enable = true;
        implicitPolicyTarget = "block";
        rules = lib.concatStringsSep "\n" cfg.usbDevices.allowlist;
      };
    })
  ];
}
