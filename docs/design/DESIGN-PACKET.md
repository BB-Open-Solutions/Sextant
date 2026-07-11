# Sextant — design packet (paste this whole file to Claude Design, then add your design system)

> Self-contained. Three parts: (1) the brief, (2) the full UI spec,
> (3) the API contract. The app is functionally complete and
> server-rendered; the task is a distinctive visual UI, not new
> behaviour. Hold the invariants; everything else is free.

---

# PART 1 — BRIEF

What to feed a designer to build the Sextant console UI, and where it
lives. The app is functionally complete and server-rendered; the job is
a visual reskin, not new behaviour. Everything here is in this repo.

## Feed this, in order

1. **`docs/design/0002-ui-spec.md`** - THE primary artifact. Information
   architecture, every screen (purpose, data, states, actions, the role
   each needs), and the cross-cutting invariants (role-gated visibility,
   one-shot secret reveal, provenance display, catalog-driven settings
   form, auditable-commit framing, i18n + per-user timezone). It also
   says what a designer may change freely vs must keep.

2. **`internal/http/api/openapi.json`** - the frozen data contract. Every
   screen mirrors these 36 endpoints (list below). A SPA is a drop-in
   client; the designer can rebuild the front end entirely against this.

3. **`internal/http/web/templates/*.html`** - the CURRENT structure to
   reskin. 13 screens + `layout.html` (shell/nav) + `login.html`. These
   show exactly what data each page renders today; a designer restyles
   or replaces them.

4. **`internal/http/web/static/app.css`** - the current (minimal) styling
   to replace. `htmx.min.js` is the interaction layer (keep or drop for
   a SPA).

5. **`docs/capabilities.md`** - what the product does, in plain terms
   (11 capabilities), for the designer's mental model.

6. **`docs/design/0001,0003,0004,0005`** - specific flows that need
   thoughtful UX: SB/TPM2 enrollment wizard (0001), remote actions /
   red-zone (0004), and the future cells admin plane (0005). Feed these
   when designing those particular screens.

## The 13 screens (template -> purpose)

| Template | Screen | Role-gated |
|---|---|---|
| overview.html | fleet health at a glance | any |
| devices.html | device list (enroll, filter) | any (scoped) |
| device.html | device detail: identity, observed, effective config, manage, apps, **security posture wizard**, **remote-action red-zone**, facts | any (scoped) |
| groups.html | group tree (create/re-parent/remove) | any (scoped) |
| settings.html | catalog-driven settings per scope + apps | any (scoped) |
| policies.html | policies + assignments + filters editors | any (scoped) |
| changes.html | review queue (open/edit/build/merge) | org viewer |
| diff.html | unified diff before merge | org viewer |
| rollout.html | staged rollout + ring-plan editor | org viewer |
| access.html | role bindings + four-eyes toggle + LDAP picker | any (scoped) |
| audit.html | config commit trail + evidence export | org viewer |
| profile.html | personal settings: prefs (tz/lang) + own API tokens | any |
| layout.html | shell: top nav, profile menu, i18n | - |

## The API surface (what the UI drives)

```
GET    /api/v1/me                         who am I + roles per scope
GET    /api/v1/me/preferences             PUT to change tz/lang
GET    /api/v1/fleet                      whole document (scope-filtered)
GET    /api/v1/devices        /{tag}      list / detail
POST   /api/v1/devices                    enroll
PATCH  /api/v1/devices/{tag}              update fields
POST   /api/v1/devices/{tag}/retire  /reactivate  /credential
POST   /api/v1/devices/{tag}/intent       lock/wipe   DELETE = cancel
GET    /api/v1/status /{tag}   /facts/{tag}   observed plane
POST   /api/v1/groups   PATCH/DELETE /{name}
POST   /api/v1/settings  DELETE           per-scope config
PUT    /api/v1/apps                       package/flatpak/overlay lists
PUT    /api/v1/policies/{id}  DELETE      policies
POST   /api/v1/assignments    DELETE      bind policy -> scope
PUT    /api/v1/filters/{id}   DELETE      filters
GET/POST/DELETE /api/v1/access            role bindings
PUT    /api/v1/assurance                  four-eyes toggle
GET    /api/v1/changes /{id} /{id}/diff   review queue
POST   /api/v1/changes  /{id}/edits /submit /merge /abandon
GET    /api/v1/rollout    POST /tick  DELETE   PUT /rollout/plan
GET    /api/v1/tokens   POST   DELETE /{id}     personal tokens
GET    /api/v1/audit                      commit trail
GET    /api/v1/evidence?from=&to=         audit bundle download
GET    /api/v1/directory/groups?q=        LDAP group picker
```

## Non-negotiable invariants (tell the designer)

- **Role-gated visibility**: never show a control the user's role at that
  scope can't use. A group-A owner must not learn group B exists.
- **One-shot secret reveal**: device credentials and minted tokens appear
  exactly once, in a highlighted copyable panel, never in a URL. One
  reusable component, three places (enroll, re-issue, token mint).
- **Provenance everywhere**: a resolved setting shows which scope/policy
  set it and whether it is locked.
- **Catalog-driven settings**: the settings form is generated from
  catalog.json, not hand-built per option.
- **Auditable-commit framing**: every Apply/Save is a git commit that
  passed the validation gate; convey it quietly.
- **Destructive weightier than routine**: retire/remove/wipe use
  confirmation (wipe: type the tag). Red-zone styling for remote actions.
- **i18n + timezone**: all chrome through the message catalog (EN/NL);
  every timestamp in the user's zone.

## Free to change

Visual language, layout, nav grouping, componentry, and whether htmx
stays or a SPA replaces it. The `/api/v1` contract is frozen and complete
- a wild, distinctive front end is welcome as long as the invariants
above hold and every action maps to an existing endpoint.

## Run it live (for the designer)

Build/test in `README.md`; `--dev-auth` on loopback gives a synthetic
owner session against a seeded repo, so every screen renders with data
without an IdP.

---

# PART 2 — UI SPECIFICATION


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

---

# PART 3 — API CONTRACT (endpoint list; full schema in openapi.json)

```
GET     /api/v1/access                                List role bindings
POST    /api/v1/access                                Grant a role binding
DELETE  /api/v1/access                                Revoke a role binding
PUT     /api/v1/apps                                  Replace an app list at a scope
POST    /api/v1/assignments                           Assign a policy to a scope
DELETE  /api/v1/assignments                           Unassign a policy
PUT     /api/v1/assurance                             Configure audit controls
GET     /api/v1/audit                                 Configuration audit trail
GET     /api/v1/changes                               List change requests
POST    /api/v1/changes                               Open a change request
GET     /api/v1/changes/{id}                          One change request
POST    /api/v1/changes/{id}/abandon                  Abandon a change
GET     /api/v1/changes/{id}/diff                     Unified diff the change would apply
POST    /api/v1/changes/{id}/edits                    Apply a gated edit on the change branch
POST    /api/v1/changes/{id}/merge                    Merge a ready change (owner)
POST    /api/v1/changes/{id}/submit                   Run the build gate
GET     /api/v1/devices                               List devices
POST    /api/v1/devices                               Enroll a device
GET     /api/v1/devices/{tag}                         Device with resolved configuration
PATCH   /api/v1/devices/{tag}                         Update device fields
DELETE  /api/v1/devices/{tag}                         Remove a device
POST    /api/v1/devices/{tag}/credential              Re-issue a device credential
POST    /api/v1/devices/{tag}/intent                  Arm a remote action
DELETE  /api/v1/devices/{tag}/intent                  Clear a remote action
POST    /api/v1/devices/{tag}/reactivate              Reactivate a retired device
POST    /api/v1/devices/{tag}/retire                  Retire a device
GET     /api/v1/directory/groups                      Browse IdP directory groups
GET     /api/v1/evidence                              Audit evidence export
GET     /api/v1/facts/{tag}                           Hardware facts of one device
PUT     /api/v1/filters/{id}                          Create or replace a filter
DELETE  /api/v1/filters/{id}                          Delete a filter
GET     /api/v1/fleet                                 Full fleet document
POST    /api/v1/groups                                Create a group
PATCH   /api/v1/groups/{name}                         Update a group
DELETE  /api/v1/groups/{name}                         Remove a group
GET     /api/v1/me                                    Who am I
GET     /api/v1/me/preferences                        My preferences
PUT     /api/v1/me/preferences                        Update my preferences
PUT     /api/v1/policies/{id}                         Create or replace a policy
DELETE  /api/v1/policies/{id}                         Delete a policy
GET     /api/v1/rollout                               Rollout state and ring convergence
POST    /api/v1/rollout                               Start a rollout (owner)
DELETE  /api/v1/rollout                               Cancel the active rollout
PUT     /api/v1/rollout/plan                          Replace the rollout ring plan
POST    /api/v1/rollout/tick                          Advance the rollout engine one step
POST    /api/v1/settings                              Set a setting at a scope
DELETE  /api/v1/settings                              Clear a setting at a scope
GET     /api/v1/status                                Observed status of every device
GET     /api/v1/status/{tag}                          Observed status of one device
GET     /api/v1/tokens                                List your API tokens
POST    /api/v1/tokens                                Mint an API token (secret shown once)
DELETE  /api/v1/tokens/{id}                           Revoke an API token
```
