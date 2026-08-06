# Delivery process: one chain from trigger to fleet

Session input for a design discussion with Bram (product owner), 2026-07-16.
Not a decision record - it frames the gaps, lays out options, and asks the
questions the session needs to answer. An ADR follows once we agree on the
shape.

Bram's brief, verbatim:

> "change a setting, or an upstream dawo update --> change request --> Test
> --> merge --> rollout"
>
> "If you update just one group you want to be able to do that in waves too.
> Right now you just drop one in."
>
> "simpler and better" / "the test process for an update is not in there" /
> "we really have to think processes like this through properly."

## 1. Problem statement

Sextant already has the pieces - `ChangeService` (change.go), the pure
rollout engine (`rollout.Decide`, rollout.go), build-before-promote
(`ports.CacheBuilder`) - but they are not one chain. Four concrete gaps:

**a. There is no TEST phase, only a build proof.** `ChangeService.Submit`
runs the eval gate and, since build-before-promote, a realisation build
(`s.builder.Build`) against the change's blast radius. That proves the
config *evaluates and compiles* - it never runs on a device. "The update was
tested" today means `Assurance.RequireTestWave` (`fleet.Assurance`,
model.go:71-74) forbidding a rollout to *start* unless its ring plan
happens to contain a `RequireApproval` ring (`RolloutPolicy.HasTestGate`,
model.go:80-90). That is a shape check on the plan, not an executed test:
nothing requires the approval-gated ring to actually have run devices on the
change before an approver signs off, and a plan can satisfy the check once
and never be revisited. Submit (proves buildable) and the test wave (proves
runs-well-on-hardware) are two different claims that today only the second
one names, and only accidentally.

**b. One global wave plan, not one per change's scope.** `RolloutPolicy.Rings`
(model.go:247-252) is a single org-wide ladder, read by `RolloutService.rings()`
(rollout.go:195-212) and driven by `RolloutService.Start(ctx, target, author)`
(rollout.go:215-233) - one target revision, staged through that one fixed
ladder. That is right for "ship the fleet forward." It is wrong for "update
just the finance group": today that path is `fleet.SetGroupPin` applied
directly (`RolloutService.Tick`'s `Promote` case calls it per ring, but a
standalone group update goes through `ConfigService.Apply` with no rings at
all) - one commit, one shot, no soak, no health floor, no canary. Bram's
"right now you just drop one in" names exactly this: the wave machinery
(`rollout.Ring.SoakMinutes` / `MinHealthyPercent` / `MaxDevices` /
`RequireApproval`) exists but is wired to the whole-fleet plan only. A
scoped change - one group, one device class - has no waved path at all.

**c. Governance errors are dead ends.** `ErrChangeRequestRequired`
(config.go:352-360) and the `NeedsTestWaveSkip` flag (pipeline.go:121-122,
rollout_ops.go:29,138) surface as messages or badges, but neither links to
"open a change request" or "add a test wave to the plan." An operator hits
the rule and has to know the fix.

**d. Core (upstream DAWO-NixOS) updates have no flow, no button, no
detection.** Every trigger that reaches `ChangeService` today edits
`fleet.json` inside the config repo (settings, apps, groups, access). A
core update is a `flake.lock` bump against the upstream DAWO-NixOS input -
searching the codebase, the only place `flake.lock` appears in production
code is nowhere; it is exclusively a test fixture
(`internal/adapters/nix/gate_e2e_test.go`). There is no poller, no
notification, no CR type, no UI affordance. An operator finds out a new
core landed by reading upstream commits by hand.

## 2. Proposed chain

One shape for every trigger, so "settings" and "core update" stop being
different mental models:

```
trigger -> change request -> TEST -> merge -> rollout (waves, scoped)
```

| Stage | Owner today | What changes |
|---|---|---|
| **Trigger** | `ConfigService.Apply` (direct) or `ChangeService.Open` | Direct edits stay for `RequireChangeRequest == false` orgs; everything else, including a core bump, opens a CR. |
| **Change request** | `ChangeService` (Draft -> Building -> Ready) | Unchanged shape. `Submit` keeps proving buildability (gate + realisation build) before a CR is even reviewable. |
| **TEST** | *missing* | New explicit phase, see section 3. Runs the **already-built** release (build-before-promote made it a real cache artifact) on a small, real device set before or immediately after merge. |
| **Merge** | `ChangeService.Merge` | Unchanged: four-eyes (`Assurance.RequireFourEyes`), gate re-validation on the merged result, rollback on failure. |
| **Rollout** | `RolloutService` + `rollout.Ring` | Waves become a property of *the change's target scope*, not only the one global plan - see below. |

**Scoped waves.** The fix for gap (b) is not a second rollout engine - `rollout.Ring`,
`rollout.Decide`, `RolloutService.Tick` are scope-agnostic already (a `Ring.Group`
is just a device group; nothing requires it to be an org-wide tier). What is
missing is a way to hand the engine a plan *derived from the change's blast
radius* instead of always reading `fleet.Rollout.Rings`. Two viable shapes:

1. **Derive a plan from the org ladder, restricted to the scope.** If the
   change touches group `finance`, and `finance` (or its ancestry) appears in
   ring 2 of the org plan, run a one-ring (or few-ring) plan scoped to
   `finance` with that ring's gates - the org's soak/health defaults still
   apply, just narrowed to the affected devices instead of the whole tier.
2. **A per-change wave plan**, expressed the same way as `RolloutPolicy.Rings`
   but attached to the `change.CR` (or requested at merge time): "roll this
   out to `finance` in 2 waves of 25%, canary first." Defaults (soak, health
   floor) come from the org policy; the operator only names *percentages or
   counts* and *how many waves*, not gate mechanics - "simpeler en beter."

Recommendation: (2), generated from (1)'s defaults. Concretely: `RolloutRing`
gains an optional `Percent` alongside the existing `MaxDevices` (count) -
today only counts are supported (`Ring.Cohort`, `Ring.NextRelease`,
rollout.go:55-88); percentages are the natural unit for "update a group in
waves" and can be resolved to a count at plan-build time (`percent * len(scope)`).
`RolloutService.Start` gains a scope parameter; when scope is narrower than
"all groups in the org plan," it synthesizes a `[]rollout.Ring` of one (small
canary wave, default soak/health, default no approval) or two rings (canary +
rest) from the org's defaults, rather than requiring the operator to hand-author
a ladder for a single-group change. An operator who needs something other than
the default only overrides what differs - "defaults for soak/healthy, only
diverge when needed," per the brief.

## 3. The TEST phase: three shapes, a recommendation

The one open design question that changes the shape of everything else: does
"tested" mean *before* the change is real (merged, on `main`), or is
promoting the merged revision through the fleet's own first ring the test?

**(a) Pre-merge: a testwave tracks the CR branch.**
Before `Merge`, a small device set (a `testwave` group, or the CR's own
sample from `gateScope`) is pointed - via the ring-branch funnel
(`RolloutService.moveRingRef`, ADR 0011) - directly at the CR's branch tip
instead of at a merged revision. Build-before-promote already makes this a
substituted release, not a per-device compile, so this is cheap even for a
weak testwave device. `Merge` becomes reachable only once the testwave has
converged healthy for its soak window - a precondition alongside four-eyes,
not a rollout ring.

- *Pro*: catches a bad change before it ever reaches `main` - no rollback
  needed, `main` stays deployable at all times.
- *Con*: needs a new "point a ring at a branch, not a revision" capability
  (today `RingBranch`/`SetRef` always target a resolved revision); the
  testwave device fleet must be idle and available on every CR, competing
  across concurrent CRs.

**(b) Post-merge: ring 0 = testwave with mandatory approval.**
Merge lands as today. Every rollout - whole-fleet or scoped - starts with a
ring 0 that *is* the test wave (`RequireApproval: true` always injected as
the first ring of any synthesized plan, not just optionally present as it is
now). `RequireTestWave` (gap a) becomes structural instead of a plan-shape
check: the engine cannot promote past ring 0 without it, because ring 0 is
never absent.

- *Pro*: no new mechanism - this is exactly what `rollout.Decide`'s
  `AwaitApproval` already does; it only needs to be non-optional. `main`
  reflects every merged change immediately (matches "config-as-data, git is
  truth").
- *Con*: a bad change is briefly real on `main` (mitigated by `Merge`'s
  existing gate-and-rollback and by four-eyes, but not eliminated) and by
  every idle ring's `FollowHead` (rollout.go:165-192) unless test devices are
  excluded from follow-HEAD groups.

**(c) Hybrid: pre-merge gate proof stays (Submit), post-merge ring 0 is the
device test.**
Keep (a)'s spirit only as far as `Submit` already goes (gate + build - config
*can* run), and make (b) the actual hardware test. This is closest to what
exists today, just with `RequireTestWave` made structural per (b) and clearly
named as a distinct board phase (section 5) so it stops reading as one more
rollout ring among many.

**Recommendation: (c), i.e. (b) with (a)'s proof kept where it already is.**
Reasoning: (a) requires a genuinely new primitive (ring-tracks-branch) for a
benefit - never landing a bad change on `main` - that the existing gate +
rollback + four-eyes combination already covers for the *evaluatable*
failure mode; what it doesn't cover is a hardware-only failure (a driver
regression, a service that fails to start), and only running on real devices
catches that, which needs a merged, buildable, cache-resident release in the
first place. (b) gets there with machinery that already exists
(`RequireApproval`, `AwaitApproval`, build-before-promote), made mandatory
instead of optional. The remaining risk of (b) - a bad change briefly on
`main` - is bounded (rollback on gate failure, four-eyes, and no ring except
0 ever follows an unproven revision) and is the trade-off to put in front of
Bram explicitly rather than assume.

## 4. Upstream core updates

Today: no detection, no CR, no UI. Proposed flow, matching the trigger shape
from section 2:

1. **Detection.** A poller (new small service, or a periodic job alongside
   `RolloutService.Run`'s ticker) watches the DAWO-NixOS repo's default
   branch. On a new commit unseen before, it raises a notification
   (`notify.Notification`, the same mechanism `ChangeService`/`RolloutService`
   already use for `ApprovalNeeded`/`GateFailed`/`RolloutDone`) - "core
   DAWO-NixOS updated: `<short-sha>` / `<date>`" - to the org's approvers.
2. **One-click CR.** The notification links to an action that calls
   `ChangeService.Open` with a generated id/title, then `Edit`s the branch's
   `flake.lock` (a new `fleet.Mutation`-equivalent for the lockfile, since
   today `Edit` only mutates `fleet.json` - see below) to the new input
   revision via `nix flake lock --update-input dawo-nixos` (or the Go
   equivalent) on the CR's worktree. One click from notification to an open,
   buildable CR.
3. **Changelog / diff for the reviewer.** `ChangeService.Diff` already
   returns the branch's diff against its base (change.go:318-327) - for a
   core bump that diff is the `flake.lock` hash change, which is not
   reviewable on its own. Fetch and attach the upstream commit range
   (old-rev..new-rev) as a changelog alongside the lockfile diff, the same
   way `Diff` is rendered on the CR page today, so an approver reviews
   "what changed upstream," not a hash.
4. **Whole-fleet radius.** A core bump's blast radius is every device (it
   changes the base module set everyone imports) - `gateScope` (change.go:349-362)
   falls back to `f.Representatives()` (the equivalence-class sample, see
   `docs/handbook/src/architecture/scale.md`) exactly for this case, so
   `Submit` already stays interactive (dozens of classes, not thousands of
   hosts) instead of evaluating the whole fleet synchronously. What is
   missing is that the *full* per-host proof - not just the sampled
   representatives - must complete before any ring beyond the test wave
   promotes. That is what the delivery pipeline's chunk-parallel gate and
   ring builds already do structurally (scale.md's "genuinely org-wide
   change is validated asynchronously"); a core-update CR should be flagged
   so its rollout is never allowed to skip straight past ring 0 (mirrors
   section 3's "make the test wave structural," doubly so when the radius is
   the whole fleet).

Net new surface: a lockfile-mutation primitive next to `fleet.Mutation`, the
poller/notifier wiring, and a changelog fetch - no change to the gate, the
build-before-promote mechanism, or the rollout engine, which are already
scope-agnostic.

## 5. Updates-board consequences

The board (`pipeline.go`) already renders one continuous strip: CR columns
(Draft/Building/Ready) followed by wave columns
(`waveCol`, pipeline.go:19-30). To make the chain from section 2 legible:

- **Insert a TEST column** between Ready and the first production wave,
  distinct from an ordinary `waveCol` even though it is implemented as ring 0
  (section 3c) - a different visual treatment (icon/label "Test wave", not
  "Wave 1") so an operator reads it as a phase, not one more step in an
  arbitrary ladder. `waveCol.Manual` already exists to flag
  `RequireApproval`; a `TestPhase bool` alongside it changes the label/badge
  without touching the underlying data.
- **Scoped rollouts get their own row/lane**, not just the org ladder: when a
  merge triggers a scoped plan (section 2's synthesized rings), the board
  shows that plan's waves next to - not instead of - the org-wide lane, since
  the two can run concurrently on disjoint device sets. This is new UI
  surface; today `pipelinePage` reads exactly one `f.Rollout` plan
  (pipeline.go:68-97).
- **Governance messages link to their fix.** `ErrChangeRequestRequired` links
  to "open a change request" (pre-filled with the edit the operator just
  attempted, where feasible); `NeedsTestWaveSkip` links to "add a test wave"
  on the plan editor rather than only rendering as a badge - closing gap (c).
- **Core-update entries render distinctly** from ordinary CRs (a different
  card treatment - "Core update: DAWO-NixOS `<sha>`" - carrying the changelog
  link from section 4 point 3), so an approver does not have to open the CR
  to know it is not a settings tweak.

## 6. Open questions for Bram

1. **Section 3 recommendation ((b): mandatory ring-0 test wave, post-merge)**
   - agree, or is "never let an unproven change touch `main`" (option a) a
   hard requirement despite the extra branch-tracking mechanism it needs?
2. Who/what are **testwave devices** - a dedicated always-on group per org
   (real hardware, held out of every ring plan), or a rotating sample drawn
   from each affected group itself (so "testing" and "canary" become the same
   wave)?
3. For a **scoped rollout** (one group, in waves): should its wave plan be
   fully operator-authored per change, or always *derived* from org defaults
   (percent + wave count only), with hand-authored ladders reserved for the
   whole-fleet plan?
4. Should `RequireTestWave` become **structural** (ring 0 always exists,
   cannot be configured away) with only "skip for this rollout" remaining as
   an explicit, logged owner action - or should an org still be able to
   configure a plan with zero test rings at all?
5. For **core updates**: auto-open a CR the moment upstream ships (fully
   automated up to "ready for review"), or only notify and leave `Open` to a
   human click? Automated CR creation changes what "an approver reviews
   before anything happens" means.
6. Does a **core-update rollout ever get to skip the whole-fleet full-proof
   requirement** (section 4 point 4) for an urgent security fix, or is "full
   per-host gate proof before any ring beyond test" non-negotiable regardless
   of urgency?

## 7. Refinements from the MDM survey (17 jul, with Bram)

Lessons adopted from Intune/Autopatch, ChromeOS, ConfigMgr phased
deployments, Nebraska/FleetLock and greenboot - each mapped to the
simplicity rule in section 8:

1. **Time AND health per ring**: promotion needs both a minimum soak
   (days) and a healthy convergence percentage. Neither alone suffices.
2. **Success threshold, not perfection**: a wave promotes at >=95%
   converged-healthy (default); the remainder becomes a visible "stragglers"
   list instead of blocking the fleet.
3. **Scatter within a wave** (ChromeOS): device switches spread over the
   wave's window automatically - bandwidth, cache and blast-radius-per-minute.
4. **Max in-flight / reboot semaphore** (Nebraska, FleetLock): never more
   than N devices of a group down at once. Default derived from group size;
   the counter-example to design for: two counter desks rebooting together.
5. **Maintenance windows** per group: one field ("update outside
   HH:MM-HH:MM"), applied to switches and ceremony reboots.
6. **Pause button**: one control that freezes a rolling release org-wide.
   Telemetry-triggered auto-halt can come later; the button comes first.
7. **Deadline + grace for the user-visible reboot** (Intune): postponable,
   eventually enforced, communicated on-device.
8. **Boot-health auto-rollback per device** (greenboot): a device that
   fails its health check after switching rolls back to the previous NixOS
   generation on its own and reports the failure. This is the per-device
   safety net UNDER the rings and is always on - not a setting.

## 8. The simplicity budget

Hard requirement (Bram): the tool must stay dead simple. The mechanics
above are invisible defaults, not configuration surface. An operator sees
exactly three org-level choices - the test group (ring 0), the default
wave shape (count + percentages), the maintenance window per group - and
per rollout: start, pause, progress. Everything else (scatter, thresholds,
in-flight limits, boot rollback, deadlines) ships as opinionated defaults
that only the whole-fleet plan may override.

Corollary for the six open questions of section 6: the simple option is
the chosen option - (1) merge to main with a mandatory ring-0 test wave,
(2) a fixed always-on test group, (3) derived wave plans for scoped
rollouts, (4) ring 0 structural with only a logged per-rollout owner skip,
(5) upstream updates auto-open a CR up to ready-for-review, (6) the
per-host gate proof is never skipped - urgency shortens soaks, never proof.

## 9. Upstream auto-CR (decision 5)

Phase 1 (built): the console polls the core repo (`SEXTANT_UPSTREAM_REPO`,
every 30 minutes, `git ls-remote HEAD`). A new revision stages exactly one
change request (`core-<rev12>`) and notifies the owners (approval-needed ->
/updates). The last processed revision lives in the state store, so a
restart or a manually opened CR never produces duplicates.

Phase 2 (to build): the CONTENT of the CR - the flake input bump - needs nix
and network, and therefore belongs to the gate-runner rather than the
console pod. Plan: a job type "bump" alongside the existing build jobs; the
runner checks out the CR branch, runs `nix flake update <core-input>`,
commits the lock file on the branch and reports done. After that the CR
follows the ordinary path: the gate builds, the kanban shows "ready", a
human approves (four-eyes), merge -> test wave -> ladder. Urgent core fixes
combine this with the expedited procedure (§8/expedited): shorter soak, same
proof.
