# Sextant Fleet Handbook

Sextant is a declarative control plane for fleets of NixOS devices.
Configuration is data in a git overlay; Nix turns that data into system
closures; devices pull and converge (comin). Sextant is the human and API
surface that edits the data safely, proves it builds, stages the rollout, and
reports what each device actually runs. It is not MDM: declarative pull, no
live command channel, every change an audited git commit.

Sextant does not ship the devices' operating system - your overlay does, on top
of a NixOS core. The core it was built against is
[DAWO](https://code.overheid.nl/MinBZK/DAWO-NixOS), the Dutch government's open
workplace image, and the two are developed together. Nothing here is specific
to it: an overlay that publishes `dawo.*` options is what Sextant configures,
whatever core provides them.

This handbook is for operators running a fleet and for engineers working on
Sextant itself. It is built with mdBook and served self-hosted; it uses no
external CDN.

## The lifecycle at a glance

1. **Set up an imaging station** - a NUC or mini-PC that boots devices over the
   network and reports what it sees to the console.
2. **Image a device** - the console dispatches an image job; the station runs
   the install and reports progress until the device is on disk and converging.
3. **Manage it** - configuration flows organisation -> group -> device; every
   change passes the Nix gate and can be reviewed as a change-request.
4. **Update it** - a rollout ships a new revision in waves. A wave promotes on
   evidence: enough of its devices reachable for a percentage to mean
   anything, enough of those healthy on the new revision, and a soak on top.
   Not on a timer, and optionally not without someone signing off.
5. **Retire or wipe it** - an audited intent the device acts on locally; a
   crypto-wipe destroys the disk's keys, and is armed per device.

To see all of that on your own machine first, clone the repository and run
`just demo`: a console, a database, sixty simulated devices and an imaging
line, gone again when you press ctrl-c. To run it for real, start with
[Install and configure Sextant](./operators/deploy.md).

For the hardware end, start with
[setting up an imaging station](./operators/station-nuc.md).
