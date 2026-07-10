# ADR 0005: Settings surface is a generated catalog; the console edits data, never code

Status: accepted (2026-07-10)

## Context

Two customer scenarios must both work without the product degenerating:
(A) an organisation manages everything through the interface - groups, the
complete image composition, overlays and apps; hand-building a toggle per
app does not scale. (B) an organisation needs custom work beyond what the
standard DAWO-NixOS core offers (including foundational choices such as
the IdP). The previous console mixed these concerns in hand-written UI and
grew unmaintainable.

## Decision

One hard boundary: **the console edits data, never nix code.**

1. **Generated catalog.** Every `dawo.*` option in the core/overlay
   carries metadata (type, description, allowed scope levels, risk class,
   category). An export tool generates `catalog.json` from the real option
   set. The interface renders its settings surface entirely from the
   catalog: type -> widget, category -> section, risk class -> guard.
   Zero hand-built toggles; a new annotated option appears in the UI
   without UI work. The catalog is versioned with the core input and is
   per organisation (a different core version yields a different catalog).
2. **Apps are data.** Packages, flatpaks and overlays are additive,
   validated name lists in the fleet document (already in the domain).
   The interface offers a curated app catalog; assigning an app writes
   data, never code.
3. **Custom work is one-time engineering.** An engineer writes a nix
   module in the overlay repo through the normal git flow. The console can
   select it (overlay names are validated data). Once its options are
   annotated, they join the catalog automatically and the customer manages
   them in the interface from then on.
4. **Foundation tier.** High-blast-radius options (IdP, disk encryption,
   secure boot) are catalog options with risk class `foundation`:
   owner-only and change-request-only. Same machinery, stricter guards.

The eval gate plus the setting-key whitelist remain the injection
firewall: data can only select catalog keys, never carry nix.

## Consequences

- The catalog is a first-class contract between core, console and UI;
  the export tool and its annotations become part of the core's CI.
- Scenario A is fully served by the interface; scenario B costs one
  engineering pass and is interface-managed afterwards.
- An Intune administrator needs the interface only; every click remains
  a validated, audited git commit.
