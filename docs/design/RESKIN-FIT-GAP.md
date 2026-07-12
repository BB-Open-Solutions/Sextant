# Stitch design ↔ app: fit / gap / how we close it

Purpose: map every Stitch screen against what the app actually serves,
name the gaps, and decide how each is closed. Then we grind the checklist
("jekko mode"). Built like a senior engineer: no fake UI, no dead ends,
every control wired to a real endpoint or deliberately deferred.

Legend for "Close by":
- **WIRE** — layout exists in Stitch, just bind our data + forms.
- **BUILD** — a real, small feature the backend already supports but the
  UI never exposed → build it (net new value).
- **ADAPT** — Stitch shows a concept our model expresses differently →
  map it honestly (no invented data).
- **DROP** — Stitch demo/dummy with no data and no product need → remove.
- **DEFER** — real feature, own milestone, not this pass.

---

## Per-screen fit

### Overview  (overview_sextant)
Stitch: Fleet Overview · 5 stat cards · Needs Attention (Investigate /
Acknowledge / View Logs) · Recent Device Activity table (Tag/Revision/
Phase/Status/Actions) · **Global Distribution** map · **CLI Toolbelt**.
App serves: Stats{Devices,Online,Groups,Policies,OpenChanges},
Attention{Kind,Detail}, Status{Tag,Revision,Phase,Online}.
- Stat cards, attention, device table → **WIRE** (real data).
- Attention actions Investigate/Acknowledge/View Logs → we have no
  ack/log store → **ADAPT**: single "Inspect" link to the device.
- Global Distribution map (geo node markers) → no geo data in the model.
  DECIDED: replace with a real device-status **heat-grid** (one cell per
  device, coloured online/offline/error/drift from live status) →
  **BUILD** (G8). Honest data, keeps the visual richness.
- CLI Toolbelt (terminal snippets) → **ADAPT**: show real, copyable
  `sxctl` commands (genuinely useful, static, no fake data).

### Devices  (devices_sextant)
Stitch: Device Fleet header · Fast Enrollment card (Asset Tag / Hardware
Profile [select] / Device Class / Primary Group). **No device list table
in the Stitch main.**
App serves: enroll (tag/hardware/class/group) + a full device list.
- Enrollment card → **WIRE** (our fields). Hardware is free text
  (overlay-specific profiles), not a fixed select → **ADAPT** input.
- Device list table → **BUILD** in the Stitch card/table style (Stitch
  omitted it; the screen needs it).

### Device detail  (device_detail_sextant)
Stitch: hostname header · Effective Configuration (Setting Key/Resolved
Value/Provenance/Enforced) · Managed Application State · Security Posture ·
Hardware Facts · Manage Device (Set Class/Assign User/Set Groups/Re-issue
Credential) · **Danger Zone** (Lock/Remote Wipe/Retire-Remove).
App serves: all of it — resolved config w/ provenance, apps, posture
wizard, facts, manage, credential-once, remote-action red zone.
- Full **WIRE**. Danger Zone = our remote-action intents; posture already
  built. Strong 1:1 fit — this is the hero and it maps cleanly.

### Groups  (groups_sextant)
Stitch: Fleet Groups · **two-pane**: Organization Hierarchy (tree, search)
+ **Group Details** panel · Sync IdP · Create Group · (top: Review Diff /
Deploy).
App serves: group tree Rows{Name,Depth,Parent,IdpGroup,Pin,Devices},
create/reparent/idp-map/remove.
- Tree + create + manage → **WIRE** into the hierarchy pane.
- Group Details right pane → **BUILD**: on select, show the group's
  settings link, device count, IdP mapping, pin, child count (all data
  we have) — nice upgrade over one flat table.
- Search groups → **BUILD** (client-side filter, small).
- Sync IdP button → not our model (bindings are config-as-data) →
  **DROP** (directory browse already lives on Access).
- Top bar Review Diff / Deploy → belongs to changes/rollout → **DROP**
  here (keep those on their own screens).

### Settings  (settings_sextant + settings_provenance_sextant)
Stitch: Configuration Editor · scope selector (Global/Group/Device as
buttons) · sections (Network Security / System Telemetry / Additive
Applications) · Enforce toggle · Apply/Clear · **Current Scope Status**
side widget. The _provenance variant adds the Effective/Source columns for
device scope.
App serves: catalog-driven Sections, Apps lists, enforce/apply/clear,
provenance on device scope.
- Both variants → our one dynamic template (org hides provenance, device
  shows it) → **WIRE**. Scope selector as segmented buttons → **ADAPT**
  (buttons over the current dropdown).
- Current Scope Status widget → **ADAPT**: show the real resolved scope
  summary (counts), no fake data.

### Policies  (policies_sextant)
Stitch: Policies · policy cards · Create Filter / Create Policy / Add
Assignment / Apply Rule / Edit.
App serves: policies (CRUD), assignments, filters, rule rows.
- Full **WIRE** — the editors map directly to policy/assignment/filter
  cards.

### Changes  (changes_sextant)
Stitch: Review Queue · change items · Comment / Merge / copy.
App serves: Changes{ID,Title,Status,Author,Updated}, open/stage-edit/
build/merge/abandon, diff.
- Review queue + merge → **WIRE**. Add our build/abandon/stage-edit +
  diff link in the Stitch item style.
- Comment on a change → no comment store → **DEFER** (small feature, own
  step) or **DROP** for 1.0; recommend DEFER-noted, not shown yet.

### Rollout  (rollout_sextant)
Stitch: Active Rollout · Progression bar · Convergence by Ring (Ring/
Strategy/Total/On Target/Status) · Ring Plan Editor · Pause/Halt/Force
Promote/Export.
App serves: State{Target,Status,Ring}, Rings{convergence}, plan editor,
start/tick/cancel.
- Status card + ring table + plan editor → **WIRE**.
- Progression bar → **BUILD** (derive % from rings promoted / total —
  real data).
- Pause → we have cancel, not pause → **ADAPT** (Halt = cancel). Force
  Promote → **ADAPT** (= tick/advance). Strategy column → **ADAPT**
  (show soak + health gate). Export → **DROP** (evidence export covers
  audit; rollout export is not a feature).

### Access  (access_sextant)
Stitch: Access Control (RBAC) · Active Role Bindings (IdP Group/Role/
Scope/**Assurance**) · Grant Access (group/role/scope + Require 4-Eyes) ·
Stage Binding.
App serves: Bindings{Group,Role,Scope}, grant, global four-eyes toggle,
directory (DirGroups) picker.
- Bindings table + grant + directory picker → **WIRE**.
- Per-binding "Assurance/Require 4-Eyes" column → our four-eyes is a
  single org-wide control, not per-binding → **ADAPT**: show the global
  four-eyes state + toggle (per-binding assurance = DEFER, real feature).

### Audit  (audit_sextant)
Stitch: Audit Logs · Configuration Commit Trail (Timestamp/Author/Subject/
Hash) · **Evidence Export** (From Date / To Date / Download JSON Bundle).
App serves: commit trail (Entries) AND `/api/v1/evidence?from&to` — but
the console never exposed evidence.
- Commit trail → **WIRE**.
- Evidence Export panel → **BUILD**: from/to inputs + a download link to
  the evidence endpoint. Backend already exists → pure UI win. High value.

### Profile  (profile_sextant + profile_api_tokens_sextant)
Stitch: User Profile · Identity · My Roles (Scope/Role) · Preferences
(Timezone/Language) · My API Tokens (Name/ID Prefix/Created/Expires/Last
Used/Actions) · Create Personal Token (Name/Ceiling/TTL) · Copy · one-shot
secret.
App serves: identity, roles, prefs, tokens list + mint (one-shot secret).
- Full **WIRE**. "Edit Identity" → identity is IdP-sourced, not editable
  → **DROP** that button.
- Copy buttons on secrets → need a tiny clipboard helper → **BUILD** a
  ~15-line vanilla-JS copy (no framework); progressive-enhancement, works
  without JS (the code stays selectable).

### Login  (login_sextant)
Stitch: Welcome back · Sign in with SSO. Full **WIRE** (done).

### States  (empty_loading_states_sextant, ui_states_popups_sextant)
Skeleton / empty ("No devices found") / loading / toast / dropdown /
error components. → **BUILD** as reusable partials: empty-state block,
and a flash/toast for post-action feedback (we redirect after POST today;
a toast on the landing page is a nice upgrade). Loading skeletons are low
value for SSR (pages arrive rendered) → **DROP** skeletons, keep empty +
error + toast.

### Admin plane  (admin_panel_sextant_1, _2)
Stitch: cells / tenant / global-admin dashboards. No backend
(instance-per-tenant provisioning = ADR 0009, not built). → **DEFER** to
the cells milestone; do not ship a dead screen now.

---

## Gap ledger (the checklist to grind)

BUILD (new UI over existing backend — real value):
- [ ] G1. Audit → Evidence Export panel (from/to + download JSON).
- [ ] G2. Devices → the fleet list table in Stitch style.
- [ ] G3. Groups → two-pane with a Group Details panel + client search.
- [ ] G4. Rollout → progression bar from real ring data.
- [ ] G5. Overview → real CLI toolbelt (sxctl) + clipboard copy helper.
- [ ] G6. Profile → clipboard copy on tokens/secret (shared helper).
- [ ] G7. Reusable partials: empty-state + flash/toast after POST.

WIRE (literal Stitch layout + our data/forms), per screen:
- [ ] W1 overview  · W2 devices · W3 device detail · W4 groups
- [ ] W5 settings (+provenance) · W6 policies · W7 changes
- [ ] W8 rollout · W9 access · W10 audit · W11 profile · W12 login/states

ADAPT (honest mapping, decided above):
- [ ] A1. Attention actions → Inspect link.
- [ ] A2. Settings scope selector → segmented buttons + real scope status.
- [ ] A3. Rollout Pause/Force-Promote/Strategy → cancel/tick/soak+health.
- [ ] A4. Access per-binding assurance → global four-eyes state.

DROP (dummy / not our model):
- [ ] D1. Overview Global Distribution map. D2. Groups Sync-IdP +
  Review-Diff/Deploy top bar. D3. Rollout Export. D4. Profile Edit
  Identity. D5. Loading skeletons.

DEFER (real features, own milestones) — see design roadmap below:
- [ ] F1. Change comments. F2. Per-binding assurance. F3. Admin-plane /
  cells (ADR 0009).

Decisions (2026-07-12): clipboard = small vanilla JS (progressive
enhancement); overview map → real status heat-grid (G8); show only what
exists (no fake UI); deferred features tracked in the roadmap below.

## Design roadmap — features the Stitch design implies but we have not
built yet (each its own milestone, backend + UI):

- **R1. Change collaboration** — comments/discussion on a change request
  before merge (Stitch shows a Comment action). Needs a comment store +
  UI thread. Value: review workflow for teams.
- **R2. Per-binding assurance** — require four-eyes on a specific role
  binding, not only org-wide (Stitch shows an Assurance column per
  binding). Needs the assurance model to move from a global flag to a
  per-binding attribute; UI already implied.
- **R3. Admin plane / cells (ADR 0009)** — the instance-per-tenant
  provisioning dashboards (admin_panel_1/2): global status across cells,
  tenant provisioning over the platform GitOps repo. Largest piece;
  backend does not exist yet.
- **R4. Rollout progression history / export** — beyond the live bar, a
  timeline of past rollouts and a per-rollout evidence slice.
These are NOT built in the reskin pass; they are the post-reskin design
backlog.

## Execution order (jekko)

1. Foundation partials (G7) + copy helper (G5/G6 shared) — used by all.
2. Screen-by-screen literal Stitch WIRE, folding in the ADAPT/DROP calls,
   green tests + deploy per batch so it is judged live:
   overview → devices → device → settings → policies → groups →
   changes → rollout → access → audit → profile.
3. BUILD the net-new panels in their screen (G1 audit, G2 devices,
   G3 groups, G4 rollout, G5 overview).
4. States (empty/toast) woven in as encountered.
DEFER items tracked, not built this pass.
