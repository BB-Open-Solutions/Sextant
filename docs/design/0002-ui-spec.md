# Design 0002: UI specification for the console

Status: spec for a designer. The console is functionally complete
(server-rendered html/template + htmx); this describes WHAT every screen
holds and HOW it behaves so a designer can reskin it without reading Go.
It does not prescribe visual style - it fixes structure, states, and
behaviour.

## Product in one paragraph

Sextant is a control plane for fleets of NixOS devices. Configuration is
data in a git overlay; nix turns it into signed system closures; devices
pull and converge (comin). The console edits that data safely, proves it
builds (the nix gate), stages rollout, and reports what each device
actually runs. It is NOT MDM: declarative pull, no live remote control
(remote lock/wipe is a separate audited intent, not a shell).

Mental model to preserve in the design: **every change is a git commit**
(auditable, reversible), and **what you can do is what your role at that
scope allows** (Odoo-style: your rights bound your API token's rights).

## Users and roles

Roles are per scope (org / group / device), highest wins along the
chain: **viewer** (read) < **editor** (change settings) < **owner**
(access, policies, merges, rollout, destructive). A user can be owner of
one group and see nothing of a sibling group (read-confidentiality: they
must not even learn it exists). The design must never show a control the
current role cannot use - hide, don't disable-with-tooltip, for
cross-scope things; for same-scope insufficient-rights, an inline
message is fine.

## Information architecture

Top nav, left to right (some items hidden by role):

- **Overview** - fleet health at a glance
- **Devices** - the machines
- **Groups** - the group tree
- **Settings** - catalog-driven config per scope
- **Policies** - reusable setting bundles + assignments + filters
- **Changes** *(org viewer only)* - the review queue
- **Rollout** *(org viewer only)* - staged updates
- **Access** - who has which role where
- **Audit** *(org viewer only)* - the commit trail
- profile menu (top-right, the user's name) - personal settings

Rationale for grouping, if the designer restructures: Devices+Groups =
"the fleet"; Settings+Policies = "what they run"; Changes+Rollout =
"how change ships"; Access+Audit = "governance"; profile = "me". A
sidebar or grouped nav is welcome; keep the role-gating.

## Screens

Each screen: purpose, key data, states, actions (with the role needed).

### Overview
- Purpose: is the fleet healthy, what needs attention.
- Data: counts (devices, online, groups, policies, open changes); an
  "attention" list (device errors, failed changes).
- States: empty fleet (onboarding hint); all-green; attention items
  present (surface prominently).

### Devices (list)
- Data per row: tag, class, hardware, groups, online dot, deployed
  revision.
- Filter/scan by group. Scoped users see only their visible devices.
- Action: enroll (editor at the target scope). Enroll is a small form
  (tag, hardware profile, optional group, class).
- **Critical flow - credential-once**: right after enroll (and on
  re-issue/reactivate) the device page shows the per-device credential
  EXACTLY ONCE in a highlighted panel with "store this now, not shown
  again". Design this panel to be unmissable and copyable. It is
  delivered via a one-shot cookie; never appears in a URL.

### Device (detail)
- Sections: Identity (groups, class, hardware, assigned user); Observed
  (online, revision, phase, last seen, error); Effective configuration
  (resolved settings with provenance = which scope/policy set each, and
  a lock badge for enforced); Manage (editor only: set class / assigned
  user / group membership; re-issue credential; retire; reactivate;
  remove); Apps (resolved packages/flatpaks/overlays); Hardware facts
  (collapsed nixos-facter doc).
- **Security posture panel** (to build, design/0001): a stepwise
  checklist toward Secure Boot + TPM2, one highlighted next action.
- States: active; retired (badge, only reactivate/remove offered);
  no check-in yet; credential-once banner present.
- Destructive actions (retire/remove) use a confirm; the design should
  make destructive clearly weightier than routine.

### Groups
- The group tree, depth-indented, with device counts, IdP mapping, pin.
- Owner actions: create (under any parent), re-parent (cycle-safe),
  map to an IdP group, remove (only empty leaves - the server refuses
  otherwise, surface that error inline).
- A scoped viewer sees only their subtree; orphan-visible subtrees
  render at root so nothing vanishes.

### Settings (catalog-driven)
- Scope selector (org / a visible group / a device). For a device,
  each row also shows the effective value + provenance.
- The entire form is generated from catalog.json (the documented dawo.*
  options); NO hand-built controls. Per option: label, description,
  a widget by type (toggle / number / select / text), an inherited-
  default hint, and a **risk badge** for high-risk options. Per option
  an **enforce** checkbox (lock the value for everything below).
- "Apply" per row; "Clear" to inherit. An untouched row must not write.
- Apps-at-this-scope panel: three comma lists (packages/flatpaks/
  overlays), names only.
- Empty-catalog state: a hint to export one.

### Policies
- A policy = a named bundle of settings + which keys it locks.
- Owner: create/edit (key = value lines; documented keys are type-
  checked), delete (refused while assigned - surface inline).
- Assignments: bind a policy to org or a group, optional filter,
  priority; unassign inline from the policy row.
- Filters: up to a few rules (attribute / operator / value), match
  all/any; delete.
- This is the Intune-parity heart; give it room.

### Changes (org viewer)
- A review queue (kanban-ish or table): id, title, status (draft ->
  building -> ready/failed -> merged/abandoned), author, updated.
- Editor: open a change, stage edits on its branch (scope/key/value or
  clear), build. Owner: merge (four-eyes: the author cannot merge their
  own when the org requires it - surface that refusal clearly).
- Diff view: the unified diff an approver reads before merge.

### Rollout (org viewer)
- Current run: target revision, status (active/halted/completed),
  current ring, per-ring convergence (on-target / total, healthy).
- Owner: start (target revision), advance-now, cancel; and a ring-plan
  editor (ordered rings: group, soak minutes, min-healthy %). Saving
  replaces the whole plan; empty clears it.
- Concept to convey: rings promote in order, each must soak healthy
  before the next starts.

### Access
- Role bindings: IdP group -> role -> scope; revoke.
- Owner: grant. If the LDAP directory is configured, the group field
  offers real IdP groups (a picker); otherwise free text. Design both.
- Assurance toggle: require four-eyes (org owner).

### Audit (org viewer)
- The config commit trail: when, who (name + email), what (commit
  subject), short hash. This is the newest slice; full history lives in
  git.
- Evidence export (to add to this screen): a from/to picker producing a
  downloadable JSON bundle for auditors.

### Profile (top-right menu) - the personal-settings anchor
- Identity + IdP groups + the user's effective roles per visible scope.
- Preferences: timezone (IANA, with common suggestions) and language
  (EN/NL); empty inherits the org default. These change how the whole
  console renders time and labels for this user.
- My API tokens: list (name, ceiling, created, expires, last used),
  create (name, optional ceiling that only narrows, ttl), revoke own.
  A freshly minted token secret shows ONCE (same one-shot pattern as
  device credentials) - design that panel to match.

## Cross-cutting behaviour

- **i18n**: all chrome runs through a message catalog (EN source, NL
  translation); the `html lang` follows the user's locale. New strings
  go through it (`.L.T "key"`), never hardcoded.
- **Time**: every timestamp renders in the user's timezone. No raw UTC
  in the UI.
- **One-shot secrets**: device credentials and minted tokens appear
  exactly once, via a highlighted panel, never in a URL, and the page
  carrying one is never cached. Design a consistent "secret reveal"
  component reused in three places (enroll, re-issue, token mint).
- **Destructive vs routine**: retire, remove, wipe (future), revoke
  read visually heavier and require confirmation (wipe: typed tag).
- **Provenance everywhere**: wherever a resolved value shows, show where
  it came from (scope or policy) and whether it is locked.
- **Empty and error states**: every list has an empty state; server
  refusals (gate rejection, "group still referenced", "cannot merge own
  change") surface as readable inline messages, not raw 500s.
- **Forms commit to git**: reinforce subtly that Apply/Save = an audited
  commit that passes the validation gate (e.g. a quiet note on write
  forms).

## What the designer can change freely vs must keep

Free: all visual language, layout, navigation grouping, componentry,
whether htmx stays or a SPA replaces it (the `/api/v1` contract is
frozen and complete - a SPA is a drop-in client).

Keep: the role-gated visibility rules, the one-shot secret behaviour,
provenance display, the catalog-driven (not hand-built) settings form,
and the auditable-commit framing. These are product invariants, not
styling.

## Reference

Routes and their data live in `internal/http/web/*.go`; the machine
contract every screen mirrors is `internal/http/api/openapi.json`. A
designer wanting live data can run the console against a seeded repo
(see README build/test) with `--dev-auth` on loopback.
