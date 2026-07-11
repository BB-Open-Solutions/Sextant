# Prompt to lead with (paste this, then DESIGN-PACKET.md, then DESIGN-mintlify.md)

You are designing the UI for **Sextant** — a control plane for fleets of
NixOS devices used by audited organisations (config-as-data in git, built
by nix, deployed by pull; explicitly NOT MDM, no live remote control
beyond an audited lock/wipe intent).

Two documents follow:
- **DESIGN-PACKET** — the product's screens, states, actions per role,
  the frozen API contract, and the non-negotiable UX invariants.
- **DESIGN-mintlify** — the visual design system to apply (palette,
  type, tokens, components).

**Task:** produce a distinctive, polished, cohesive UI for the console —
all 13 screens — in the Mintlify visual language, honouring every
invariant in the packet (role-gated visibility, one-shot secret reveal,
provenance display, catalog-driven settings form, red-zone for
destructive actions, auditable-commit framing, EN/NL + per-user
timezone). This is a reskin of a functionally complete app: every action
must map to an existing endpoint in the contract — invent no new
behaviour, only its visual expression.

**Priorities:**
1. The daily-driver screens first: Overview, Devices (list + detail with
   the security-posture wizard and remote-action red-zone), Settings
   (the catalog-driven form is the heart), Policies.
2. The governance screens: Changes/diff, Rollout, Access, Audit.
3. Personal: the profile menu (preferences + own API tokens), including
   the one-shot secret reveal component reused in three places.

**Deliverables you should aim for:** a design language (color/type/space
applied to this domain), the shell (nav + profile menu + i18n), each
screen as a composed layout with its empty/loading/error/role-gated
states, and the reusable components (secret-reveal panel, provenance
badge, risk badge, destructive-confirm, status/online indicators,
scope selector, the settings widget-by-type).

Adapt the Mintlify system to an application console (not a marketing
site): favour its dense-documentation surfaces, hairlines, mono for
identifiers/revisions/commit hashes, the green for active/healthy states,
and reserve strong warm/error tones for destructive and attention paths.
