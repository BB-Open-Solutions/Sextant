# 0014 - Custom overlays authored in the console

## Status

Proposed. Grounds the design for item 3 (custom overlays for IoT / k8s / POS
device classes, authored and managed from the interface). No code yet.

## Context

The generator already supports **overlays**: named Nix modules in the overlay
repo (`overlays/<name>.nix`, passed to `mkFleet` as `overlaysDir`), which a
scope selects through `Scope.Overlays []string` (additive across the org ->
group -> device chain). Today an operator adds an overlay by editing the repo
by hand; the console can only *select* an existing overlay, not author one.

The goal: let an operator write a custom overlay in code from the console -
for an IoT gateway, a k8s node, a POS terminal - and then deploy and manage it
through the same gated, audited flow as the rest of the configuration.

## Decision

Overlays become first-class, console-authored config-as-code:

- **Authoring.** A console Overlays surface (owner only) lists the overlay
  modules and offers a code editor (a plain server-rendered textarea; no
  inline scripts, CSP stays clean) to create, edit and delete
  `overlays/<name>.nix`.
- **Gated write.** Writing an overlay uses the same safe-write transaction as
  fleet.json: write the file, run the Nix eval gate over the affected hosts
  (the module must evaluate and the resulting system must build), commit with
  SSO-attributed authorship on success, roll back on failure. An overlay that
  does not evaluate never reaches git.
- **Assignment.** Scopes select overlays through the existing `Scope.Overlays`
  mechanism (org / group / device), so a "k8s-node" overlay is assigned to the
  k8s group, a "pos-terminal" overlay to the POS devices, and so on. Device
  classes (laptop, kiosk, server, station, and new ones) give a natural home.

### Slices

1. **Gated overlay write path** (app + repo): `ConfigService.WriteOverlay` /
   `DeleteOverlay` over a new `ConfigRepo.ListFiles("overlays")`, reusing the
   gate + commit + rollback machinery. Pure-ish, unit-testable with a temp repo.
2. **Console Overlays page**: list + code editor + create/edit/delete, owner
   gated, with the eval error surfaced on a bad module.
3. **Class templates**: starter overlays per device class (IoT / k8s / POS) so
   an operator edits from a working base rather than a blank file.

## Consequences

- **Trust.** A custom overlay is arbitrary Nix that the generator imports -
  effectively root on the devices that select it. So authoring is owner-only,
  every change is an audited commit, and the eval gate + staged rollout (waves,
  health, soak) contain a bad overlay before it reaches the whole fleet. This
  is the same trust model as the rest of config-as-code; the console does not
  widen it, it just makes authoring safe and auditable instead of hand-edited.
- The gate proves the overlay *evaluates and builds*, not that it is *correct*;
  a canary wave (ADR 0013) is the real safety net for behaviour.
- Overlays are additive and per-scope, so the same base image gains only the
  modules its scope selects - no separate image per class.
