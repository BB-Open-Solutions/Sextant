# UI spec: updates and rollout (for the Stitch redesign)

Goal: make update management as clear as the imaging station. The model is
Intune/Autopatch (proven), the interface slicker, plus our own features
(soak, thresholds, pause, stragglers, boot rollback). See
docs/design/delivery-process.md §7-8 for the decisions; this document
describes the screens only.

## Vocabulary (matters for every screen)

Two kinds of change, two journeys:

- **Updates** (core/image: a new NixOS release, security patches) touch ALL
  devices -> the full rollout ladder: test devices first, then percentage
  waves.
- **Changes** (settings: org/group/device scope) touch only their scope ->
  test wave first, then only the affected scope. No fleet ladder.

The per-group ladder disappears from the UI as a primary concept; groups are
an implementation detail of the percentage waves (a wave consists of whole
groups, smallest first, until the percentage is reached).

## Screen 1: Org -> "Update policy" tile (/org/updates)

Set and forget; three choices and nothing else. Structure (numbered steps):

1. **Card "Test devices"** (step 1)
   - One select: pick the test group. Help text: "These devices always get
     every update first, on real hardware, with manual sign-off before the
     fleet follows. Usually IT's own machines."
   - A badge shows the current test group and its device count.
2. **Card "Rollout ladder"** (step 2)
   - A percentage input with presets as clickable chips: `10 · 30 · 60`
     (recommended), `10 · 20 · 30 · 40`, `25 · 75`, and "custom split" (free
     field). One button: "Derive plan".
   - **Live plan preview** below it: one row per wave with the wave name
     ("Wave 1 · 10%"), the groups that fall into it, the device count and
     the REAL percentage (group granularity ≈ the requested percentage). The
     test wave sits on top, visually distinct (green accent plus a sign-off
     icon).
3. **Card "Maintenance window"** (step 3 - UI still to be built)
   - A window per group, "HH:MM-HH:MM" (already exists as the setting
     dawo.updates.maintenanceWindow); defaults to "always".
4. **Card "Governance"** - the three checkboxes (change request required,
   four-eyes, test wave required). Stays as it is.
5. **Details "Advanced"** - the manual wave ladder (for fleet-wide
   exceptions only). Collapsed, deliberately inconspicuous.

Invisible defaults (NOT in the UI): threshold 95%, soak 60/30 min, scatter,
max-in-flight, boot-health rollback (no off switch).

## Screen 2: Sidebar "Updates" (/updates) - overview

- At the top: a summary card for the running rollout (badge
  Active/Paused/-, the active wave, a "View rollout" button), or a start
  button when nothing is running.
- On starting: show what is being rolled out and whether it is an **update**
  (whole fleet, full ladder) or a **change** (scope X, test wave plus that
  scope).
- Below that: the changes kanban (CRs) as it is now.

## Screen 3: /updates/rollout - monitoring

- Status line plus Approve/Pause/Resume/Stop.
- Wave cards in the wizard idiom: the active wave marked, a progress bar, a
  "Now:" line in plain language, a stragglers expander (device plus reason).
- Wave labels are the ladder names ("Test group", "Wave 1 · 10%", ...).

## Engine (already built, 17 July)

- A wave can span several groups (`groups` alongside `group`).
- Plan derivation: test group plus percentages -> waves of whole groups,
  smallest first (`derivePlan`), validated (a group appears in one wave).
- Convergence and stragglers count across all groups in the wave.

## Still to build (after Stitch)

- Chips/presets (today: one text field), the maintenance-window card,
  change-vs-update classification when starting, scoped rollout derivation
  (test wave plus the affected scope only), #88 auto-CR for upstream
  updates.
