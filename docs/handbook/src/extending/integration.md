# Build your own integration

An integration in Sextant is not a plugin. There is no SDK to learn, no
interface to implement and no code to add to the console. An integration is a
**NixOS module in your overlay that publishes options**, and the console picks
them up because they are in the catalog.

That is the whole mechanism. If you can write a NixOS module, you can add an
integration, and the console will render it, validate it, scope it, gate it and
audit it for you.

This page walks through one from nothing to working.

## What you are actually building

Two things:

1. **Options** - `options.dawo.<yourthing>` with types, defaults and
   descriptions. These become the fields an operator fills in.
2. **Config** - what those options *do* on the device: a systemd unit, a
   package, a file, whatever the thing needs.

Everything else is done for you:

| You do not write | Because |
|---|---|
| A console form | The catalog carries the type; the console renders the field. |
| Validation | The Nix gate evaluates the change before it can be committed. |
| Scoping | Settings resolve org → group → device like any other setting. |
| An audit trail | Every save is a git commit with an author. |
| Secret handling | An annotation turns a field into a secret-ref picker. |

## Step 1: write the module

Put it in your overlay, next to the modules you already have. A minimal
integration - reporting to a metrics collector - looks like this:

```nix
# modules/telemetry.nix
{ config, lib, pkgs, ... }:
let
  cfg = config.dawo.telemetry;
in
{
  options.dawo.telemetry = {
    enable = lib.mkEnableOption "report metrics to a collector";

    endpoint = lib.mkOption {
      type = lib.types.str;
      default = "";
      example = "https://metrics.example.org/ingest";
      description = "Collector the agent posts to.";
    };

    intervalSeconds = lib.mkOption {
      type = lib.types.ints.between 30 3600;
      default = 300;
      description = "How often to report, in seconds.";
    };

    # The annotation is what makes this a secret-ref picker in the console
    # rather than a free-text box. See "Secrets" below.
    token = lib.mkOption {
      type = lib.types.str;
      default = "";
      description = "Secret-ref name of the collector's bearer token.";
    } // { secret = true; };
  };

  config = lib.mkIf cfg.enable {
    assertions = [{
      assertion = cfg.endpoint != "";
      message = "dawo.telemetry: endpoint must be set when enabled.";
    }];

    systemd.services.dawo-telemetry = {
      serviceConfig.Type = "oneshot";
      script = ''
        ${pkgs.curl}/bin/curl -sS -X POST "${cfg.endpoint}" \
          -H "Authorization: Bearer $(cat /run/agenix/${cfg.token})" \
          --data-binary @/proc/loadavg
      '';
    };

    systemd.timers.dawo-telemetry = {
      wantedBy = [ "timers.target" ];
      timerConfig = {
        OnBootSec = "2min";
        OnUnitActiveSec = "${toString cfg.intervalSeconds}s";
      };
    };
  };
}
```

Three things in there are worth copying every time.

**Types do real work.** `lib.types.ints.between 30 3600` is not decoration: the
console renders it as a bounded field, the catalog type-check rejects 5 before
Nix ever runs, and the gate rejects it again if somebody edits `fleet.json` by
hand. A type is the cheapest validation you will ever write.

**Descriptions are the UI.** The `description` is what an operator reads in the
console. Write it for them, not for you: "Collector the agent posts to" beats
"the endpoint".

**Assert what enabling implies.** `enable = true` with an empty `endpoint` is a
configuration that cannot work. An assertion turns that from a device that
silently does nothing into a change the gate refuses, with the reason attached.

## Step 2: add it to your class's module list

The module has to be in the image the class builds. In the BB Open overlay that
is `coreModulesForClass`; in yours it is wherever you assemble the NixOS module
list per device class.

This is also where class-scoping happens. A module you add only to the laptop
class publishes options tagged as laptop-only, and the console will tell an
operator that setting them on a station reaches nothing - rather than letting
them configure something that will never apply.

## Step 3: regenerate the catalog

`catalog.json` is derived output. The console reads it, not your Nix source, so
until you regenerate it your integration does not exist as far as the console is
concerned.

```sh
nix eval .#catalog --json > catalog.json
```

Commit it with the module. In this repository CI enforces that they match
(`examples/overlay/regen-catalog.sh --check`), and it is worth having the same
guard in your overlay: a stale catalog is a console showing an option set that
the fleet no longer has.

## Step 4: use it

Open **Settings** in the console. Your options are there, under their key names,
with the types and descriptions you wrote. Set them at the org, a group or one
device. Save; the gate builds it; it becomes a commit; the ring rolls it out.

That is the integration finished. No console change, no restart, no deployment.

## Secrets

Never put a credential in an option value. It would land in `fleet.json`, in
git, in every clone, forever.

Annotate the option `// { secret = true; }` instead. The console then renders a
**picker of registered secret names**, and what gets stored is the name. On the
device, agenix decrypts the material to `/run/agenix/<name>` and your module
reads it from there - which is why the example above does
`cat /run/agenix/${cfg.token}` rather than interpolating a value.

Register the name first on the Secrets page, then point the field at it. See
[Manage secrets](../operators/secrets.md).

## Marking a dangerous option

Some options change the security posture of a device. Annotate those:

```nix
    disableFirewall = lib.mkOption {
      type = lib.types.bool;
      default = false;
      description = "Stop filtering inbound traffic.";
    } // { riskClass = "high"; };
```

The console shows a warning badge and asks for an explicit extra confirmation
before saving. Use it for what genuinely deserves it; a badge on everything is a
badge on nothing.

## Getting a card on the Integrations page

Options alone give you a fully working integration in **Settings**. The
**Integrations** page is a curated shortcut on top of that - a card per
integration, configured at org scope in one place - and its list lives in the
console's source (`internal/http/web/integrations.go`, `knownIntegrations`).

So:

- **Adding options to your overlay** needs no console change and is the normal
  case. Your integration works, in Settings, scoped like everything else.
- **A card on the Integrations page** needs a four-line entry in that list, which
  means a pull request to Sextant itself. Worth doing for an integration that
  many fleets will run; not worth it for one that is specific to yours.

If you build something other organisations would use, send the card entry with
it. That is one of the more welcome contributions there is.

## What does not belong here

Some integrations are not device-side at all: the console's own SSO, its
directory lookups, its outbound mail. Those are adapters behind ports
(`internal/adapters/`), configured at deploy time rather than per fleet, because
they belong to the console's operator and not to the fleet's configuration.

The test is simple: **if every device would need to know about it, it is an
overlay module. If only the console needs to know, it is an adapter.**

## Checklist

- [ ] Options under `dawo.<name>`, with types that describe the real range.
- [ ] A `description` on every option, written for the operator.
- [ ] An assertion for each way that enabling it could be incoherent.
- [ ] Secrets as `// { secret = true; }` refs, read from `/run/agenix/<name>`.
- [ ] The module in the right class's module list.
- [ ] `catalog.json` regenerated and committed.
- [ ] Enabled on one device first, and actually checked on the hardware.
