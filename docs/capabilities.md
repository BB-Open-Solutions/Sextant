# Sextant capabilities

The product contract: what belongs in this control-plane, where each
capability stands, and the boundaries that keep it disciplined. Every
capability lands with the same rigor: spec first, pure domain, ports,
adapters, /api/v1, then UI. The UI is a client of the API, never more.

## The one rule

**The console edits data, never nix code.** Anything expressible as
validated data (settings, app selections, group membership, bindings)
belongs in the interface. Anything requiring code (new modules, hardware
profiles) is one-time engineering in git - and once annotated, it becomes
data the interface manages from then on. See ADR 0005.

## Capability map

| # | Capability | What it covers | Status |
|---|-----------|----------------|--------|
| 1 | Identity and access | OIDC SSO, per-scope RBAC (viewer/editor/owner), audited attribution | Built |
| 2 | Configuration management | Scope tree (org -> groups -> device), policies + assignments + filters, enforce semantics, eval gate, git audit trail | Domain built; becomes real with the v3 nix generator |
| 3 | Enrollment and lifecycle | Device lifecycle: discovered -> enrolled -> active -> retired; group management | Enroll built; lifecycle states and retire pending |
| 4 | Status and inventory | Check-ins, online/offline, deployed revision, drift, hardware facts (nixos-facter) | Basis built; facter enrichment and drift detection pending |
| 5 | Updates and rollout | Flake input updates -> change request -> build gate -> staged rings with soak and health gates | Rollout engine built; update funnel pending |
| 6 | Remote actions | Declarative intents: lock, cryptographic wipe, retire (see below) | Design pending |
| 7 | Provisioning (inspoelstraat) | Stations, PXE discoveries -> enroll queue, image builds | Not yet ported from the PoC |
| 8 | Compliance | Posture against profiles (BIO), comply-or-explain register | Deliberately later; acceptance register exists in the domain |
| 9 | Multi-organisation | Model B: one overlay repo per org, org provisioning | Storage tenant-ready; runtime later |

## Remote actions: honest semantics

A declarative pull model cannot perform a classic MDM wipe (an offline
device executes nothing). What Sextant offers instead, and states plainly:

- **Lock**: account disabled and mesh key revoked at the next converge.
- **Cryptographic wipe**: revoke the LUKS keyslot / TPM enrollment; data
  becomes unreachable at the next boot.
- **Retire**: remove from the fleet, rotate every secret the device held.

All remote actions are declarative intents recorded as gated, audited
commits - never imperative agent commands. No remote code execution
channel exists by design.

## Settings surface (summary of ADR 0005)

Three tiers, one machinery:

1. **Catalog-driven settings** - every annotated `dawo.*` option is
   exported to `catalog.json` and rendered by the interface automatically.
   Apps are data lists (packages, flatpaks, overlays), additive across the
   scope chain; the interface is a curated app catalog, not hand-built
   toggles.
2. **Custom work** - an engineer writes a nix module in the overlay repo
   (git flow, review, CI). The console can select it (an overlay name is
   validated data); its annotated options join the catalog automatically.
3. **Foundation** - IdP, disk encryption, secure boot: catalog options
   with risk class `foundation`; owner-only, change-request required.

An Intune administrator uses only the interface. Anything missing there is
one-time engineering, after which it is manageable in the interface.

## Discipline (the anti-mess rules)

- No capability without a spec and ADR first.
- Pure domain, exhaustively tested; effects behind ports.
- Everything through /api/v1; the UI is a client.
- Per-scope RBAC on every mutation; every change is a gated git commit.
- Files stay small (~400 lines); CI green or no merge.
