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

---

# PART 4 — CURRENT TEMPLATES + CSS (the structure to reskin)

These are the live server-rendered screens today (Go html/template).
They show exactly what data each page renders and how it nests -
reskin or replace them. Template syntax: {{.Field}} = a value,
{{range}} = a list, {{if}} = conditional; the packet's screen table
maps each file to its purpose.

## layout.html (shell: nav, profile menu, i18n)
```html
{{define "layout"}}<!DOCTYPE html>
<html lang="{{.L.Locale}}">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>{{.Title}} - Sextant</title>
  <link rel="stylesheet" href="/static/app.css">
  <script src="/static/htmx.min.js" defer></script>
</head>
<body>
<header>
  <span class="brand">Sextant</span>
  <nav>
    <a href="/" {{if eq .Nav "overview"}}class="active"{{end}}>{{.L.T "nav.overview"}}</a>
    <a href="/devices" {{if eq .Nav "devices"}}class="active"{{end}}>{{.L.T "nav.devices"}}</a>
    <a href="/groups" {{if eq .Nav "groups"}}class="active"{{end}}>{{.L.T "nav.groups"}}</a>
    <a href="/settings" {{if eq .Nav "settings"}}class="active"{{end}}>{{.L.T "nav.settings"}}</a>
    <a href="/policies" {{if eq .Nav "policies"}}class="active"{{end}}>{{.L.T "nav.policies"}}</a>
    {{if .CanOrgView}}
    <a href="/changes" {{if eq .Nav "changes"}}class="active"{{end}}>{{.L.T "nav.changes"}}</a>
    <a href="/rollout" {{if eq .Nav "rollout"}}class="active"{{end}}>{{.L.T "nav.rollout"}}</a>
    {{end}}
    <a href="/access" {{if eq .Nav "access"}}class="active"{{end}}>{{.L.T "nav.access"}}</a>
    {{if .CanOrgView}}<a href="/audit" {{if eq .Nav "audit"}}class="active"{{end}}>{{.L.T "nav.audit"}}</a>{{end}}
  </nav>
  <span class="who">
    <a href="/profile" {{if eq .Nav "profile"}}class="active"{{end}}>{{.User.Name}}</a>
    <form class="inline" method="post" action="/logout">
      <input type="hidden" name="csrf" value="{{.CSRF}}">
      <button class="quiet" type="submit">{{.L.T "nav.signout"}}</button>
    </form>
  </span>
</header>
<main>
  {{with .Error}}<div class="error">{{.}}</div>{{end}}
  {{template "content" .}}
</main>
</body>
</html>{{end}}
```

## overview.html
```html
{{define "content"}}
<h1>Overview</h1>
<div class="grid">
  <div class="panel"><div class="stat">{{.Stats.Devices}}</div><div class="muted">Devices</div></div>
  <div class="panel"><div class="stat">{{.Stats.Online}}</div><div class="muted">Online</div></div>
  <div class="panel"><div class="stat">{{.Stats.Groups}}</div><div class="muted">Groups</div></div>
  <div class="panel"><div class="stat">{{.Stats.Policies}}</div><div class="muted">Policies</div></div>
  <div class="panel"><div class="stat">{{.Stats.OpenChanges}}</div><div class="muted">Open changes</div></div>
</div>

{{if .Attention}}
<h2>Needs attention</h2>
<div class="panel">
  <table>
    <tr><th>What</th><th>Detail</th></tr>
    {{range .Attention}}
    <tr><td><span class="tag bad">{{.Kind}}</span></td><td>{{.Detail}}</td></tr>
    {{end}}
  </table>
</div>
{{end}}

<h2>Device status</h2>
<div class="panel">
  <table>
    <tr><th>Device</th><th>Revision</th><th>Phase</th><th>Seen</th></tr>
    {{range .Status}}
    <tr>
      <td><a href="/devices/{{.Tag}}">{{.Tag}}</a></td>
      <td>{{.Revision}}</td>
      <td>{{.Phase}}</td>
      <td>{{if .Online}}<span class="tag ok">online</span>{{else}}<span class="tag bad">offline</span>{{end}}</td>
    </tr>
    {{else}}
    <tr><td colspan="4" class="muted">No check-ins yet.</td></tr>
    {{end}}
  </table>
</div>
{{end}}
```

## devices.html
```html
{{define "content"}}
<h1>Devices</h1>

{{if .CanEdit}}
<div class="panel">
  <form method="post" action="/devices">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="tag" placeholder="asset tag (slug)" required pattern="[a-z0-9][a-z0-9-]*">
    <input name="hardware" placeholder="hardware profile" required>
    <input name="class" placeholder="class (laptop/server/station)" size="14">
    <select name="group">
      <option value="">no group</option>
      {{range .Groups}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
    <button type="submit">Enroll device</button>
    <span class="muted">Validated by the gate, committed to git.</span>
  </form>
</div>
{{end}}

<div class="panel">
  <table>
    <tr><th>Tag</th><th>Groups</th><th>Class</th><th>Hardware</th><th>User</th><th>Status</th></tr>
    {{range .Devices}}
    <tr>
      <td><a href="/devices/{{.Tag}}">{{.Tag}}</a></td>
      <td>{{range .Groups}}<span class="tag">{{.}}</span> {{end}}</td>
      <td>{{.Class}}</td>
      <td>{{.Hardware}}</td>
      <td>{{.AssignedUser}}</td>
      <td>{{if .HasStatus}}{{if .Online}}<span class="tag ok">online</span>{{else}}<span class="tag bad">offline</span>{{end}} {{.Revision}}{{else}}<span class="muted">never seen</span>{{end}}</td>
    </tr>
    {{end}}
  </table>
</div>
{{end}}
```

## device.html
```html
{{define "content"}}
<h1>Device {{.Tag}} {{if .Retired}}<span class="tag bad">retired</span>{{end}}</h1>

{{with .Credential}}
<div class="panel" style="border-color:#3a6">
  <h2 style="margin-top:0">Device credential</h2>
  <p><strong>Store this credential now; it is not shown again.</strong>
  The device uses it to check in as itself.</p>
  <p><code>{{.}}</code></p>
</div>
{{end}}

<div class="grid">
  <div class="panel">
    <h2 style="margin-top:0">Identity</h2>
    <table>
      <tr><td class="muted">Groups</td><td>{{range .Device.Groups}}<span class="tag">{{.}}</span> {{end}}</td></tr>
      <tr><td class="muted">Class</td><td>{{.Device.Class}}</td></tr>
      <tr><td class="muted">Hardware</td><td>{{.Device.Hardware}}</td></tr>
      <tr><td class="muted">Assigned user</td><td>{{.Device.AssignedUser}}</td></tr>
    </table>
  </div>
  <div class="panel">
    <h2 style="margin-top:0">Observed</h2>
    {{if .HasStatus}}
    <table>
      <tr><td class="muted">State</td><td>{{if .Status.Online}}<span class="tag ok">online</span>{{else}}<span class="tag bad">offline</span>{{end}}</td></tr>
      <tr><td class="muted">Revision</td><td>{{.Status.Revision}}</td></tr>
      <tr><td class="muted">Phase</td><td>{{.Status.Phase}}</td></tr>
      <tr><td class="muted">Last seen</td><td>{{$.L.TimeSec .Status.LastSeen}}</td></tr>
      {{with .Status.Error}}<tr><td class="muted">Error</td><td class="tag bad">{{.}}</td></tr>{{end}}
    </table>
    {{else}}<p class="muted">No check-in received.</p>{{end}}
  </div>
</div>

{{with .Posture}}
{{if or .WantSB .WantTPM2}}
<h2>Security posture</h2>
<div class="panel">
  {{if not .Reported}}
  <p class="muted">Waiting for a posture-aware check-in from the device agent.</p>
  {{else}}
  <table>
    {{if .WantSB}}
    <tr><td class="muted">Secure Boot</td>
      <td>
        {{if eq (printf "%s" .SB) "enforcing"}}<span class="tag ok">enforcing</span>
        {{else if eq (printf "%s" .SB) "audit"}}<span class="tag">audit mode</span>
        {{else if eq (printf "%s" .SB) "off"}}<span class="tag bad">off</span>
        {{else}}<span class="muted">unknown</span>{{end}}
      </td></tr>
    {{end}}
    {{if .WantTPM2}}
    <tr><td class="muted">TPM2 unlock</td>
      <td>
        {{if eq (printf "%s" .TPM2) "enrolled"}}<span class="tag ok">enrolled</span>
        {{else if eq (printf "%s" .TPM2) "present"}}<span class="tag">present, not bound</span>
        {{else if eq (printf "%s" .TPM2) "absent"}}<span class="tag bad">absent</span>
        {{else}}<span class="muted">unknown</span>{{end}}
      </td></tr>
    {{end}}
  </table>
  {{if .Complete}}
  <p><span class="tag ok">complete</span> {{.StepText}}</p>
  {{else}}
  <p><strong>Next:</strong> {{.StepText}}
    {{if .Warn}}<span class="tag bad">attention</span>{{end}}</p>
  {{end}}
  {{if $.CanEdit}}
  {{range .Actions}}
  <form method="post" action="/devices/{{$.Tag}}/posture" class="inline">
    <input type="hidden" name="csrf" value="{{$.CSRF}}">
    <input type="hidden" name="action" value="{{.Action}}">
    <button type="submit" {{if .Quiet}}class="quiet"{{end}}>{{.Label}}</button>
  </form>
  {{end}}
  {{end}}
  {{end}}
</div>
{{end}}
{{end}}

<h2>Effective configuration</h2>
<div class="panel">
  <table>
    <tr><th>Setting</th><th>Value</th><th>Source</th><th></th></tr>
    {{range .Resolved}}
    <tr>
      <td>{{.Key}}</td>
      <td><code>{{printf "%v" .Value}}</code></td>
      <td class="provenance">{{.Source}}</td>
      <td>{{if .Enforced}}<span class="tag lock">locked</span>{{end}}</td>
    </tr>
    {{else}}
    <tr><td colspan="4" class="muted">Nothing configured yet.</td></tr>
    {{end}}
  </table>
</div>

{{if .CanEdit}}
<h2>Manage</h2>
<div class="panel">
  <form method="post" action="/devices/{{.Tag}}/update" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input type="hidden" name="setclass" value="1">
    <input name="class" value="{{.Device.Class}}" placeholder="class (laptop, kiosk)">
    <button type="submit" class="quiet">Set class</button>
  </form>
  <form method="post" action="/devices/{{.Tag}}/update" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input type="hidden" name="setuser" value="1">
    <input name="assignedUser" value="{{.Device.AssignedUser}}" placeholder="assigned user">
    <button type="submit" class="quiet">Assign</button>
  </form>
  <form method="post" action="/devices/{{.Tag}}/update" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input type="hidden" name="setgroups" value="1">
    <select name="groups" multiple size="3">
      {{range .GroupOpts}}
      <option value="{{.Name}}" {{if .Member}}selected{{end}}>{{.Name}}</option>
      {{end}}
    </select>
    <button type="submit" class="quiet">Set groups</button>
  </form>
  <hr>
  {{if .Retired}}
  <form method="post" action="/devices/{{.Tag}}/reactivate" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <button type="submit">Reactivate</button>
    <span class="muted">returns to service with a fresh credential</span>
  </form>
  {{else}}
  <form method="post" action="/devices/{{.Tag}}/credential" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <button type="submit" class="quiet">Re-issue credential</button>
  </form>
  <form method="post" action="/devices/{{.Tag}}/retire" class="inline"
        onsubmit="return confirm('Retire {{.Tag}}? Builds and check-ins stop; the record stays for audit.')">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <button type="submit" class="quiet">Retire</button>
  </form>
  {{end}}
  <form method="post" action="/devices/{{.Tag}}/remove" class="inline"
        onsubmit="return confirm('Remove {{.Tag}} entirely? This unenrolls the device.')">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <button type="submit" class="quiet">Remove</button>
  </form>
</div>

<h2>Set a device setting</h2>
<div class="panel">
  <p><a href="/settings?scope=device:{{.Tag}}">Open the settings editor for this device</a>
  <span class="muted">(catalog-driven, with provenance)</span></p>
  <form method="post" action="/devices/{{.Tag}}/settings">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="key" placeholder="setting key (e.g. apps.office)" required>
    <input name="value" placeholder="value (true, false, a string)" required>
    <button type="submit">Apply</button>
    <span class="muted">Free-form key; applies through the validation gate and commits to git.</span>
  </form>
</div>
{{end}}

<h2>Apps</h2>
<div class="panel">
  <table>
    <tr><td class="muted">Packages</td><td>{{range .Packages}}<span class="tag">{{.}}</span> {{end}}</td></tr>
    <tr><td class="muted">Flatpaks</td><td>{{range .Flatpaks}}<span class="tag">{{.}}</span> {{end}}</td></tr>
    <tr><td class="muted">Overlays</td><td>{{range .Overlays}}<span class="tag">{{.}}</span> {{end}}</td></tr>
  </table>
</div>

{{if .CanOwn}}
{{if not .Retired}}
<h2>Remote actions</h2>
<div class="panel" style="border-color:#a33">
  {{if .Intent}}
  <p><strong>Armed:</strong> <span class="tag bad">{{.Intent}}</span>
    {{if and .HasStatus (eq .Status.Ack .Intent)}}<span class="tag ok">delivered</span>
    {{else}}<span class="muted">pending delivery on next check-in</span>{{end}}</p>
  <form method="post" action="/devices/{{.Tag}}/intent/clear" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <button type="submit" class="quiet">Cancel</button>
  </form>
  {{else}}
  <p class="muted" style="margin-top:0">Lost or stolen device. Lock is reversible; wipe
  destroys the disk encryption keys and is irreversible. Every action is an audited commit.</p>
  <form method="post" action="/devices/{{.Tag}}/intent" class="inline">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input type="hidden" name="intent" value="lock">
    <button type="submit">Lock device</button>
  </form>
  <form method="post" action="/devices/{{.Tag}}/intent"
        onsubmit="return confirm('WIPE {{.Tag}}? This destroys the disk encryption keys - irreversible.')">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input type="hidden" name="intent" value="wipe">
    <input type="hidden" name="force" value="1">
    <label class="muted">Type the tag to confirm a wipe:
      <input name="confirm" placeholder="{{.Tag}}" required></label>
    <button type="submit" class="quiet">Wipe device</button>
  </form>
  {{end}}
</div>
{{end}}
{{end}}

{{with .Facts}}
<h2>Hardware facts</h2>
<div class="panel">
  <p class="muted">Reported {{$.L.Time $.FactsAt}} (nixos-facter)</p>
  <details><summary class="muted">raw document</summary><pre style="max-height:24em;overflow:auto">{{.}}</pre></details>
</div>
{{end}}
{{end}}
```

## groups.html
```html
{{define "content"}}
<h1>Groups</h1>

<div class="panel">
  <p class="muted" style="margin-top:0">
    Policy resolves org &rarr; parent groups &rarr; group &rarr; device. Enforced values
    flow down; defaults may be overridden below. Re-parenting moves a whole subtree's
    governance and needs organisation owner.
  </p>
  <table>
    <tr><th>Group</th><th>IdP mapping</th><th>Pin</th><th>Devices</th><th></th></tr>
    {{$ := .}}
    {{range .Rows}}
    <tr>
      <td style="padding-left:{{.Depth}}em">
        <strong>{{.Name}}</strong>
        <br><a class="muted" href="/settings?scope=group:{{.Name}}">settings</a>
      </td>
      <td>{{with .IdpGroup}}<code>{{.}}</code>{{else}}<span class="muted">-</span>{{end}}</td>
      <td>{{with .Pin}}<code>{{.}}</code>{{else}}<span class="muted">follows HEAD</span>{{end}}</td>
      <td>{{.Devices}}</td>
      <td>
        {{if $.CanOrgOwn}}
        <details>
          <summary class="muted">manage</summary>
          <form method="post" action="/groups/{{.Name}}/update" class="inline">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <input type="hidden" name="reparent" value="1">
            <select name="parent">
              <option value="" {{if not .Parent}}selected{{end}}>(root)</option>
              {{$row := .}}
              {{range $.AllGroups}}
              {{if ne . $row.Name}}
              <option value="{{.}}" {{if eq . $row.Parent}}selected{{end}}>{{.}}</option>
              {{end}}
              {{end}}
            </select>
            <button type="submit" class="quiet">Re-parent</button>
          </form>
          <form method="post" action="/groups/{{.Name}}/update" class="inline">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <input type="hidden" name="setidp" value="1">
            <input name="idpGroup" value="{{.IdpGroup}}" placeholder="IdP group">
            <button type="submit" class="quiet">Map</button>
          </form>
          <form method="post" action="/groups/{{.Name}}/remove" class="inline">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <button type="submit" class="quiet">Remove</button>
          </form>
        </details>
        {{end}}
      </td>
    </tr>
    {{else}}
    <tr><td colspan="5" class="muted">No groups yet.</td></tr>
    {{end}}
  </table>
</div>

{{if .CanOrgOwn}}
<h2>Create a group</h2>
<div class="panel">
  <form method="post" action="/groups">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="name" placeholder="name (lowercase slug)" required>
    <select name="parent">
      <option value="">(root)</option>
      {{range .AllGroups}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
    <input name="idpGroup" placeholder="IdP group mapping (optional)">
    <button type="submit">Create</button>
  </form>
</div>
{{end}}
{{end}}
```

## settings.html
```html
{{define "content"}}
<h1>Settings</h1>

<div class="panel">
  <form method="get" action="/settings" class="inline">
    <label class="muted" for="scope">Scope</label>
    <select id="scope" name="scope" onchange="this.form.submit()">
      <option value="org" {{if eq .Scope "org"}}selected{{end}}>Organisation</option>
      {{$scope := .Scope}}
      {{range .Groups}}
      <option value="group:{{.}}" {{if eq $scope (printf "group:%s" .)}}selected{{end}}>Group: {{.}}</option>
      {{end}}
      {{if .IsDevice}}<option value="{{.Scope}}" selected>{{.Scope}}</option>{{end}}
    </select>
    <noscript><button type="submit">Switch</button></noscript>
  </form>
  <p class="muted" style="margin-bottom:0">
    Values set here apply at the selected scope. <strong>Enforce</strong> locks a value
    for everything below (most-general wins); a plain value is a default the more
    specific scope may override. Every change passes the validation gate and commits to git.
  </p>
</div>

{{if .Empty}}
<div class="panel">
  <p class="muted">No catalog published yet. Export one from the overlay repo:
  <code>nix eval .#catalog --json &gt; catalog.json</code> and commit it.</p>
</div>
{{end}}

{{$ := .}}
<h2>Apps at this scope</h2>
<div class="panel">
  <p class="muted" style="margin-top:0">Additive across the chain: a device gets the union
  of org, group ancestry and its own lists. Names only (nixpkgs attrs, flathub ids,
  repo overlays) - never code.</p>
  {{range .Apps}}
  <form method="post" action="/apps">
    <input type="hidden" name="csrf" value="{{$.CSRF}}">
    <input type="hidden" name="scope" value="{{$.Scope}}">
    <input type="hidden" name="kind" value="{{.Kind}}">
    <label class="muted" style="display:inline-block;min-width:6em">{{.Kind}}</label>
    <input name="names" value="{{.Names}}" placeholder="comma-separated" style="min-width:24em">
    {{if $.CanEdit}}<button type="submit" class="quiet">Save</button>{{end}}
  </form>
  {{end}}
</div>

{{range $sec := .Sections}}
<h2>{{$sec.Name}}</h2>
<div class="panel">
  <table>
    <tr>
      <th>Setting</th><th>Value at this scope</th><th>Enforce</th>
      {{if $.IsDevice}}<th>Effective</th><th>Source</th>{{end}}
      <th></th>
    </tr>
    {{range $i, $row := $sec.Rows}}
    {{$fid := printf "f-%s-%d" $sec.Name $i}}
    <tr>
      <td>
        <strong>{{$row.Entry.Name}}</strong>
        {{if $row.Entry.RiskClass}}<span class="tag bad">{{$row.Entry.RiskClass}} risk</span>{{end}}
        <br><span class="muted">{{$row.Entry.Description}}</span>
        {{with $row.Entry.DefaultString}}<br><span class="muted">default: <code>{{.}}</code></span>{{end}}
      </td>
      {{if $.CanEdit}}
      <td>
        {{if eq $row.Entry.Widget "toggle"}}
          <select name="value" form="{{$fid}}">
            <option value="" {{if not $row.Set}}selected{{end}} disabled>inherit</option>
            <option value="true" {{if and $row.Set (eq $row.Value "true")}}selected{{end}}>true</option>
            <option value="false" {{if and $row.Set (eq $row.Value "false")}}selected{{end}}>false</option>
          </select>
        {{else if eq $row.Entry.Widget "select"}}
          <select name="value" form="{{$fid}}">
            <option value="" {{if not $row.Set}}selected{{end}} disabled>inherit</option>
            {{range $row.Entry.Options}}
            <option value="{{.}}" {{if and $row.Set (eq $row.Value .)}}selected{{end}}>{{.}}</option>
            {{end}}
          </select>
        {{else if eq $row.Entry.Widget "number"}}
          <input type="number" name="value" form="{{$fid}}" value="{{if $row.Set}}{{$row.Value}}{{end}}" placeholder="inherit">
        {{else}}
          <input name="value" form="{{$fid}}" value="{{if $row.Set}}{{$row.Value}}{{end}}" placeholder="inherit">
        {{end}}
      </td>
      <td><input type="checkbox" name="enforce" form="{{$fid}}" {{if $row.Enforced}}checked{{end}}></td>
      {{if $.IsDevice}}
      <td>{{with $row.Resolved}}<code>{{.}}</code>{{else}}<span class="muted">-</span>{{end}}</td>
      <td class="provenance">{{$row.Source}}</td>
      {{end}}
      <td>
        <button type="submit" name="action" value="set" form="{{$fid}}">Apply</button>
        {{if $row.Set}}<button type="submit" name="action" value="clear" form="{{$fid}}" class="quiet" formnovalidate>Clear</button>{{end}}
      </td>
      {{else}}
      <td>{{if $row.Set}}<code>{{$row.Value}}</code>{{else}}<span class="muted">inherited</span>{{end}}</td>
      <td>{{if $row.Enforced}}<span class="tag lock">locked</span>{{end}}</td>
      {{if $.IsDevice}}
      <td>{{with $row.Resolved}}<code>{{.}}</code>{{else}}<span class="muted">-</span>{{end}}</td>
      <td class="provenance">{{$row.Source}}</td>
      {{end}}
      <td></td>
      {{end}}
    </tr>
    {{end}}
  </table>
  {{if $.CanEdit}}
  {{range $i, $row := $sec.Rows}}
  <form id="{{printf "f-%s-%d" $sec.Name $i}}" method="post" action="/settings">
    <input type="hidden" name="csrf" value="{{$.CSRF}}">
    <input type="hidden" name="scope" value="{{$.Scope}}">
    <input type="hidden" name="key" value="{{$row.Entry.Name}}">
  </form>
  {{end}}
  {{end}}
</div>
{{end}}
{{end}}
```

## policies.html
```html
{{define "content"}}
<h1>Policies</h1>

<div class="panel">
  <table>
    <tr><th>Policy</th><th>Settings</th><th>Locked keys</th><th>Assigned to</th>{{if .CanOwn}}<th></th>{{end}}</tr>
    {{$ := .}}
    {{range .Policies}}
    <tr>
      <td><strong>{{.ID}}</strong>{{with .Description}}<div class="muted">{{.}}</div>{{end}}</td>
      <td>{{range $k, $v := .Settings}}<div><code>{{$k}} = {{printf "%v" $v}}</code></div>{{end}}</td>
      <td>{{range .Enforced}}<span class="tag lock">{{.}}</span> {{end}}</td>
      <td>
        {{$pol := .ID}}
        {{range .Assignments}}
        <div>
          <span class="tag">{{.Target}}</span>{{with .Filter}} filter <span class="tag">{{.}}</span>{{end}}
          {{if $.CanOwn}}
          <form method="post" action="/assignments/delete" class="inline">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <input type="hidden" name="policy" value="{{$pol}}">
            <input type="hidden" name="target" value="{{.Target}}">
            <input type="hidden" name="filter" value="{{.Filter}}">
            <button type="submit" class="quiet">unassign</button>
          </form>
          {{end}}
        </div>
        {{end}}
      </td>
      {{if $.CanOwn}}
      <td>
        <details>
          <summary class="muted">edit</summary>
          <form method="post" action="/policies">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <input type="hidden" name="id" value="{{.ID}}">
            <input name="description" value="{{.Description}}" placeholder="description">
            <textarea name="settings" rows="4" placeholder="key = value">{{.SettingsText}}</textarea>
            <input name="enforced" value="{{.EnforcedText}}" placeholder="locked keys, comma-separated">
            <button type="submit">Save</button>
          </form>
          <form method="post" action="/policies/{{.ID}}/delete" class="inline">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <button type="submit" class="quiet">Delete</button>
          </form>
        </details>
      </td>
      {{end}}
    </tr>
    {{else}}
    <tr><td colspan="5" class="muted">No policies defined.</td></tr>
    {{end}}
  </table>
</div>

{{if .CanOwn}}
<h2>Create a policy</h2>
<div class="panel">
  <form method="post" action="/policies">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="id" placeholder="id (lowercase slug)" required>
    <input name="description" placeholder="description">
    <textarea name="settings" rows="4" placeholder="apps.office = true&#10;desktop = plasma" required></textarea>
    <input name="enforced" placeholder="locked keys, comma-separated (optional)">
    <button type="submit">Create</button>
    <span class="muted">One setting per line: <code>key = value</code>. Documented keys are type-checked; the gate validates the rest.</span>
  </form>
</div>

<h2>Assign a policy</h2>
<div class="panel">
  <form method="post" action="/assignments">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <select name="policy" required>
      <option value="" disabled selected>policy</option>
      {{range .PolicyIDs}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
    <select name="target" required>
      <option value="org">org</option>
      {{range .Groups}}<option value="group:{{.}}">group:{{.}}</option>{{end}}
    </select>
    <select name="filter">
      <option value="">no filter (all devices)</option>
      {{range .FilterIDs}}<option value="{{.}}">{{.}}</option>{{end}}
    </select>
    <input name="priority" type="number" placeholder="priority (tie-break)">
    <button type="submit">Assign</button>
  </form>
</div>
{{end}}

<h2>Filters</h2>
<div class="panel">
  <table>
    <tr><th>Filter</th><th>Match</th><th>Rules</th>{{if .CanOwn}}<th></th>{{end}}</tr>
    {{range .Filters}}
    <tr>
      <td><strong>{{.ID}}</strong></td>
      <td>{{.Match}}</td>
      <td>{{range .Rules}}<div><code>{{.Attr}} {{.Op}} {{.Value}}{{range .Values}}{{.}} {{end}}</code></div>{{end}}</td>
      {{if $.CanOwn}}
      <td>
        <form method="post" action="/filters/{{.ID}}/delete" class="inline">
          <input type="hidden" name="csrf" value="{{$.CSRF}}">
          <button type="submit" class="quiet">Delete</button>
        </form>
      </td>
      {{end}}
    </tr>
    {{else}}
    <tr><td colspan="4" class="muted">No filters defined.</td></tr>
    {{end}}
  </table>
</div>

{{if .CanOwn}}
<h2>Create a filter</h2>
<div class="panel">
  <form method="post" action="/filters">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="id" placeholder="id (lowercase slug)" required>
    <select name="match">
      <option value="all">all rules must hold</option>
      <option value="any">any rule suffices</option>
    </select>
    {{range $i := .RuleRows}}
    <div>
      <input name="attr{{$i}}" placeholder="attr (tag, class, hardware, assignedUser, group, label:key)">
      <select name="op{{$i}}">
        <option value="eq">eq</option><option value="ne">ne</option>
        <option value="prefix">prefix</option><option value="in">in</option>
      </select>
      <input name="value{{$i}}" placeholder="value (comma list for in)">
    </div>
    {{end}}
    <button type="submit">Create</button>
  </form>
</div>
{{end}}
{{end}}
```

## changes.html
```html
{{define "content"}}
<h1>Changes</h1>

{{if .CanEdit}}
<div class="panel">
  <form method="post" action="/changes">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="id" placeholder="change id (slug)" required pattern="[a-z0-9][a-z0-9-]*">
    <input name="title" placeholder="title" required size="40">
    <button type="submit">Open change</button>
  </form>
</div>
{{end}}

<div class="panel">
  <table>
    <tr><th>Change</th><th>Status</th><th>Author</th><th>Updated</th><th>Actions</th></tr>
    {{range .Changes}}
    <tr>
      <td><strong>{{.ID}}</strong><div class="muted">{{.Title}}</div>
        {{with .Error}}<div class="tag bad">{{.}}</div>{{end}}</td>
      <td><span class="tag {{if eq .Status "ready"}}ok{{else if eq .Status "failed"}}bad{{else if eq .Status "merged"}}ok{{end}}">{{.Status}}</span></td>
      <td>{{.Author}}</td>
      <td>{{$.L.Time .Updated}}</td>
      <td>
        {{if .Open}}<a href="/changes/{{.ID}}/diff">diff</a> {{end}}
        {{if $.CanEdit}}
        {{if or (eq .Status "draft") (eq .Status "failed")}}
        <details>
          <summary class="muted">stage an edit</summary>
          <form method="post" action="/changes/{{.ID}}/edits">
            <input type="hidden" name="csrf" value="{{$.CSRF}}">
            <select name="scope">
              <option value="org">org</option>
              {{range $.Groups}}<option value="group:{{.}}">group:{{.}}</option>{{end}}
            </select>
            <input name="key" placeholder="setting key" required>
            <input name="value" placeholder="value">
            <label class="muted"><input type="checkbox" name="clear" value="1"> clear instead</label>
            <button type="submit" class="quiet">Stage</button>
          </form>
        </details>
        <form class="inline" method="post" action="/changes/{{.ID}}/submit">
          <input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Build</button>
        </form>
        {{end}}
        {{if eq .Status "ready"}}
        <form class="inline" method="post" action="/changes/{{.ID}}/merge">
          <input type="hidden" name="csrf" value="{{$.CSRF}}"><button>Merge</button>
        </form>
        {{end}}
        {{if .Open}}
        <form class="inline" method="post" action="/changes/{{.ID}}/abandon">
          <input type="hidden" name="csrf" value="{{$.CSRF}}"><button class="quiet">Abandon</button>
        </form>
        {{end}}
        {{end}}
      </td>
    </tr>
    {{else}}
    <tr><td colspan="5" class="muted">No changes yet.</td></tr>
    {{end}}
  </table>
</div>
{{end}}
```

## diff.html
```html
{{define "content"}}
<h1>Change {{.ID}} - diff</h1>
<p class="muted">{{.Change.Title}} - {{.Change.Status}} - by {{.Change.Author}}.
This is what merging applies to the fleet configuration.</p>
<div class="panel" style="overflow-x:auto">
  {{if .Diff}}<pre style="margin:0; font-size:0.85em; line-height:1.4">{{.Diff}}</pre>
  {{else}}<p class="muted">No changes on this branch yet.</p>{{end}}
</div>
<p><a href="/changes">Back to changes</a></p>
{{end}}
```

## rollout.html
```html
{{define "content"}}
<h1>Rollout</h1>

{{if .State}}
<div class="panel">
  <table>
    <tr><td class="muted">Target</td><td><code>{{.State.Target}}</code></td></tr>
    <tr><td class="muted">Status</td><td><span class="tag {{if eq (printf "%s" .State.Status) "active"}}ok{{else if eq (printf "%s" .State.Status) "halted"}}bad{{end}}">{{.State.Status}}</span>
      {{with .State.Reason}}<span class="muted">{{.}}</span>{{end}}</td></tr>
    <tr><td class="muted">Current ring</td><td>{{.State.Ring}}</td></tr>
  </table>
  {{if .CanOwn}}
  <form class="inline" method="post" action="/rollout/tick" style="margin-top:0.8rem">
    <input type="hidden" name="csrf" value="{{.CSRF}}"><button class="quiet">Advance now</button>
  </form>
  <form class="inline" method="post" action="/rollout/cancel">
    <input type="hidden" name="csrf" value="{{.CSRF}}"><button class="quiet">Cancel run</button>
  </form>
  {{end}}
</div>

<h2>Rings</h2>
<div class="panel">
  <table>
    <tr><th>#</th><th>Group</th><th>Soak</th><th>Health gate</th><th>Convergence</th></tr>
    {{range $i, $r := .Rings}}
    <tr>
      <td>{{$i}}</td><td>{{$r.Ring.Group}}</td>
      <td>{{$r.Ring.SoakMinutes}}m</td>
      <td>{{if $r.Ring.MinHealthyPercent}}{{$r.Ring.MinHealthyPercent}}%{{else}}100%{{end}}</td>
      <td>{{$r.Status.OnTarget}}/{{$r.Status.Total}} on target, {{$r.Status.Healthy}} healthy</td>
    </tr>
    {{end}}
  </table>
</div>
{{else}}
<div class="panel"><p class="muted">No rollout run. {{if not .HasRings}}Configure rollout rings below first.{{end}}</p>
{{if and .CanOwn .HasRings}}
<form method="post" action="/rollout">
  <input type="hidden" name="csrf" value="{{.CSRF}}">
  <input name="target" placeholder="target revision" required>
  <button type="submit">Start rollout</button>
</form>
{{end}}
</div>
{{end}}

{{if .CanOwn}}
<h2>Ring plan</h2>
<div class="panel">
  <p class="muted" style="margin-top:0">Rings promote in order: a ring must converge
  healthy through its soak window before the next one starts. Saving replaces the
  whole plan; an empty form clears it.</p>
  <form method="post" action="/rollout/plan">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    {{$ := .}}
    {{range $i := .RingRows}}
    <div>
      <select name="group{{$i}}">
        <option value="">(unused ring)</option>
        {{range $.AllGroups}}
        <option value="{{.}}" {{if eq . (index $.PlanGroups $i)}}selected{{end}}>{{.}}</option>
        {{end}}
      </select>
      <input name="soak{{$i}}" type="number" min="0" placeholder="soak (min)" value="{{index $.PlanSoaks $i}}">
      <input name="healthy{{$i}}" type="number" min="0" max="100" placeholder="min healthy %" value="{{index $.PlanHealthy $i}}">
    </div>
    {{end}}
    <button type="submit">Save plan</button>
  </form>
</div>
{{end}}
{{end}}
```

## access.html
```html
{{define "content"}}
<h1>Access</h1>
<p class="muted">Role bindings grant IdP groups a role at a scope. Bindings are part of the
fleet configuration: every change is a gated, audited git commit.</p>

<div class="panel">
  <table>
    <tr><th>IdP group</th><th>Role</th><th>Scope</th><th></th></tr>
    {{range .Bindings}}
    <tr>
      <td>{{.Group}}</td>
      <td><span class="tag">{{.Role}}</span></td>
      <td><code>{{.Scope}}</code></td>
      <td>{{if $.CanOwn}}
        <form class="inline" method="post" action="/access/revoke">
          <input type="hidden" name="csrf" value="{{$.CSRF}}">
          <input type="hidden" name="group" value="{{.Group}}">
          <input type="hidden" name="scope" value="{{.Scope}}">
          <button class="quiet">Revoke</button>
        </form>{{end}}</td>
    </tr>
    {{else}}
    <tr><td colspan="4" class="muted">No bindings; only server-configured baseline groups apply.</td></tr>
    {{end}}
  </table>
</div>

{{if .CanOwn}}
<h2>Grant</h2>
<div class="panel">
  <form method="post" action="/access/grant">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="group" placeholder="IdP group" required
           {{if .DirGroups}}list="dir-groups"{{end}}>
    {{if .DirGroups}}
    <datalist id="dir-groups">
      {{range .DirGroups}}<option value="{{.Name}}">{{end}}
    </datalist>
    {{end}}
    <select name="role">
      <option value="viewer">viewer</option>
      <option value="editor">editor</option>
      <option value="owner">owner</option>
    </select>
    <select name="scope">
      <option value="org">org</option>
      {{range .Groups}}<option value="group:{{.}}">group:{{.}}</option>{{end}}
    </select>
    <button type="submit">Grant</button>
    {{if not .DirGroups}}<span class="muted">directory browse not configured; type the IdP group name</span>{{end}}
  </form>
</div>

<h2>Assurance</h2>
<div class="panel">
  <form method="post" action="/assurance">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <label><input type="checkbox" name="requireFourEyes" {{if .FourEyes}}checked{{end}}>
    Require four-eyes: a change may not be merged by its own author (segregation of duties).</label>
    <button type="submit" class="quiet">Save</button>
  </form>
</div>
{{end}}
{{end}}
```

## audit.html
```html
{{define "content"}}
<h1>Audit trail</h1>
<div class="panel">
  <p class="muted" style="margin-top:0">Every configuration change is a git commit with
  real attribution. This is the newest slice; the full history lives in the overlay
  repository.</p>
  <table>
    <tr><th>When</th><th>Who</th><th>What</th><th>Commit</th></tr>
    {{range .Entries}}
    <tr>
      <td>{{$.L.TimeSec .When}}</td>
      <td>{{.Author}} <span class="muted">{{.Email}}</span></td>
      <td>{{.Subject}}</td>
      <td><code>{{printf "%.10s" .Hash}}</code></td>
    </tr>
    {{else}}
    <tr><td colspan="4" class="muted">No history available.</td></tr>
    {{end}}
  </table>
</div>
{{end}}
```

## profile.html
```html
{{define "content"}}
<h1>Profile</h1>

{{with .MintedSecret}}
<div class="panel" style="border-color:#3a6">
  <h2 style="margin-top:0">New token created</h2>
  <p><strong>Store this secret now; it is not shown again.</strong></p>
  <p><code>{{.}}</code></p>
</div>
{{end}}

<div class="grid">
  <div class="panel">
    <h2 style="margin-top:0">Identity</h2>
    <table>
      <tr><td class="muted">Name</td><td>{{.User.Name}}</td></tr>
      <tr><td class="muted">Email</td><td>{{.User.Email}}</td></tr>
      <tr><td class="muted">Subject</td><td><code>{{.User.Subject}}</code></td></tr>
      <tr><td class="muted">IdP groups</td><td>{{range .User.Groups}}<span class="tag">{{.}}</span> {{end}}</td></tr>
    </table>
  </div>
  <div class="panel">
    <h2 style="margin-top:0">My roles</h2>
    <table>
      <tr><th>Scope</th><th>Role</th></tr>
      {{range $scope, $role := .Roles}}
      <tr><td><code>{{$scope}}</code></td><td><span class="tag">{{$role}}</span></td></tr>
      {{else}}
      <tr><td colspan="2" class="muted">No roles.</td></tr>
      {{end}}
    </table>
  </div>
</div>

{{if .HasPrefs}}
<h2>Preferences</h2>
<div class="panel">
  <form method="post" action="/profile/prefs">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <label class="muted" for="timezone">Timezone</label>
    <input id="timezone" name="timezone" value="{{.Prefs.Timezone}}"
           placeholder="organisation default (e.g. Europe/Amsterdam)" list="tz-common">
    <datalist id="tz-common">
      <option value="Europe/Amsterdam"><option value="Europe/Brussels">
      <option value="Europe/Paris"><option value="Europe/Berlin"><option value="UTC">
    </datalist>
    <label class="muted" for="locale">Language</label>
    <select id="locale" name="locale">
      <option value="" {{if not .Prefs.Locale}}selected{{end}}>organisation default</option>
      {{$cur := .Prefs.Locale}}
      {{range .Locales}}<option value="{{.}}" {{if eq $cur .}}selected{{end}}>{{.}}</option>{{end}}
    </select>
    <button type="submit">Save</button>
  </form>
</div>
{{end}}

{{if .HasTokens}}
<h2>My API tokens</h2>
<div class="panel">
  <table>
    <tr><th>Name</th><th>ID</th><th>Ceiling</th><th>Created</th><th>Expires</th><th>Last used</th><th></th></tr>
    {{$csrf := .CSRF}}
    {{range .Tokens}}
    <tr>
      <td>{{.Name}}</td>
      <td><code>{{.ID}}</code></td>
      <td>{{with .Ceiling}}<span class="tag">{{.}}</span>{{else}}<span class="muted">-</span>{{end}}</td>
      <td>{{$.L.Date .Created}}</td>
      <td>{{$.L.Date .Expires}}</td>
      <td>{{if .LastUsed}}{{$.L.TimePtr .LastUsed}}{{else}}<span class="muted">never</span>{{end}}</td>
      <td>
        <form method="post" action="/profile/tokens/{{.ID}}/revoke" class="inline">
          <input type="hidden" name="csrf" value="{{$csrf}}">
          <button type="submit" class="quiet">Revoke</button>
        </form>
      </td>
    </tr>
    {{else}}
    <tr><td colspan="7" class="muted">No personal tokens.</td></tr>
    {{end}}
  </table>

  <h3>Create a personal token</h3>
  <p class="muted">The token acts as you, with your current groups snapshotted.
  A ceiling can only narrow what it may do. The secret is shown once.</p>
  <form method="post" action="/profile/tokens">
    <input type="hidden" name="csrf" value="{{.CSRF}}">
    <input name="name" placeholder="what is this token for?" required>
    <select name="ceiling">
      <option value="">no ceiling (my full rights)</option>
      <option value="viewer">viewer</option>
      <option value="editor">editor</option>
    </select>
    <input name="ttlDays" type="number" min="1" placeholder="days (default 90)">
    <button type="submit">Create</button>
  </form>
</div>
{{end}}
{{end}}
```

## login.html
```html
{{define "login"}}<!DOCTYPE html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>Sign in - Sextant</title>
  <link rel="stylesheet" href="/static/app.css">
</head>
<body>
<div class="login-box panel">
  <h1>Sextant</h1>
  <p class="muted">Declarative fleet control-plane for NixOS.</p>
  {{if .SSO}}
  <p><a href="/login/start"><button>Sign in with SSO</button></a></p>
  {{else}}
  <p class="error">No identity provider configured.</p>
  {{end}}
</div>
</body>
</html>{{end}}
```

## static/app.css (current minimal styling to replace)
```css
/* Sextant console. One small stylesheet, system fonts, no build step. */
:root {
  --bg: #f6f7f9; --panel: #fff; --ink: #1a2330; --muted: #5b6577;
  --line: #dde2e9; --accent: #1f5bd8; --ok: #197a4b; --warn: #a15c07; --bad: #b3261e;
}
* { box-sizing: border-box; }
body { margin: 0; background: var(--bg); color: var(--ink);
  font: 15px/1.5 system-ui, -apple-system, "Segoe UI", sans-serif; }
a { color: var(--accent); text-decoration: none; }
a:hover { text-decoration: underline; }
header { background: var(--panel); border-bottom: 1px solid var(--line);
  display: flex; align-items: center; gap: 1.5rem; padding: 0 1.5rem; }
header .brand { font-weight: 700; padding: 0.9rem 0; }
header nav { display: flex; gap: 1rem; }
header nav a { color: var(--ink); padding: 0.9rem 0.2rem; border-bottom: 2px solid transparent; }
header nav a.active { border-color: var(--accent); color: var(--accent); }
header .who { margin-left: auto; color: var(--muted); font-size: 0.9em;
  display: flex; align-items: center; gap: 0.8rem; }
main { max-width: 72rem; margin: 1.5rem auto; padding: 0 1.5rem; }
h1 { font-size: 1.4rem; margin: 0 0 1rem; }
h2 { font-size: 1.05rem; margin: 1.5rem 0 0.5rem; }
.panel { background: var(--panel); border: 1px solid var(--line);
  border-radius: 8px; padding: 1rem 1.2rem; margin-bottom: 1rem; }
table { width: 100%; border-collapse: collapse; }
th { text-align: left; color: var(--muted); font-weight: 600; font-size: 0.85em;
  padding: 0.4rem 0.6rem; border-bottom: 1px solid var(--line); }
td { padding: 0.45rem 0.6rem; border-bottom: 1px solid var(--line); vertical-align: top; }
tr:last-child td { border-bottom: none; }
.tag { display: inline-block; padding: 0.1rem 0.5rem; border-radius: 99px;
  font-size: 0.8em; background: var(--bg); border: 1px solid var(--line); }
.tag.ok { color: var(--ok); border-color: var(--ok); }
.tag.warn { color: var(--warn); border-color: var(--warn); }
.tag.bad { color: var(--bad); border-color: var(--bad); }
.tag.lock { color: var(--warn); }
form.inline { display: inline; }
input, select { padding: 0.35rem 0.5rem; border: 1px solid var(--line);
  border-radius: 6px; font: inherit; background: var(--panel); }
button { padding: 0.4rem 0.9rem; border: 1px solid var(--accent); border-radius: 6px;
  background: var(--accent); color: #fff; font: inherit; cursor: pointer; }
button.quiet { background: var(--panel); color: var(--ink); border-color: var(--line); }
.grid { display: grid; grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr)); gap: 1rem; }
.stat { font-size: 1.6rem; font-weight: 700; }
.muted { color: var(--muted); }
.error { background: #fdecea; border: 1px solid var(--bad); color: var(--bad);
  border-radius: 8px; padding: 0.7rem 1rem; margin-bottom: 1rem; }
.provenance { font-size: 0.8em; color: var(--muted); }
.login-box { max-width: 22rem; margin: 6rem auto; text-align: center; }
```
