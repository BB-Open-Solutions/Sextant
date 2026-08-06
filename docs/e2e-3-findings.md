# E2e-3 findings — Sextant on its own legs (2026-08-04)

What broke, why, and what changed because of it. Every item here was found
on real hardware during Run A of `docs/e2e-acceptance-plan.md` — enrolling
and imaging a laptop through the inspoelstraat with every integration off,
so a failure has one suspect instead of four.

**A note on names, because the commits do not match the plan.** The plan
calls this run "e2e-3". The devices provisioned during it were named `e2e4`
and `e2e5`, and the commit messages and code comments use those names as
session labels. They refer to this run. When searching for the origin of a
fix, `e2e4`/`e2e5` is the string that will find it; `e2e-3` is the plan's
name for the exercise as a whole.

**Where the run stopped.** Run A reached A3.11 — the rekey with a real
admin identity — and paused there. Everything from A4 (convergence) onward
is still to do. That is not a finding, it is the state of the run, recorded
here so nobody reads a short findings list as a clean bill of health.

## The one that mattered: a device cannot exist where it is installed from

**Symptom.** Enrolling `e2e4` and imaging it died with

```
error: flake '...?rev=526ba918' does not provide attribute
       'nixosConfigurations."e2e4".config.system.build.diskoScript'
```

**Cause.** Enrolment writes the device to `main`. Imaging installs the
revision the device's ring is **pinned** to — that is `#16`, so a machine is
not born ahead of its own ring. Those two are never the same commit, and not
by accident: the engine records every promotion as a commit on `main`, so a
pin is permanently at least one commit behind. A device enrolled after the
pin therefore does not exist at the revision it is installed from.

This was structural, not an edge case. It had been latent since `#16` landed
without a hardware run — which is the entire argument for running the
acceptance plan on iron rather than in a VM.

**Why both obvious fixes are wrong.** Installing from `main` is exactly what
`#16` fixed: the device is then ahead of its ring, comin refuses a head that
is not a descendant, and the machine freezes at its image-time generation.
Cherry-picking the enrolment onto the ring branch is worse — the branch stops
being a commit on `main`'s history, so the next promotion is no longer a
fast-forward and the whole ring wedges instead of one device.

**Fixed by** (Sextant 0.79.0, commit `cfae35f`): fast-forward the covering
ring branches to the enrolment commit, and install that commit. The device
boots exactly at its ring's head.

The guard is what makes that safe. Advancing a pin also carries whatever else
sits between the old pin and the enrolment to that ring's existing devices,
with no soak and no health gate. So it proceeds only when that is provably a
no-op: every current member's configuration shape must be identical at both
revisions. Otherwise it refuses, naming the ring and the devices — an
instruction instead of an unparseable Nix error
(`internal/app/enrolment_rings.go`).

Group pins are levelled before comparing. The generator reads `dev.pin` and
never `groups.<g>.pin`, but `classKey` folds group pins in, erring toward more
classes. That is free for the verdict memo and wrong here: the engine's pin
commit always sits between the old pin and an enrolment, so without levelling
every enrolment would look like a change and every ring would refuse.

Both properties are mutation-checked: a guard that always passes fails the
refusal test, and comparing with group pins intact fails the advance test.

## A machine with failed units was called "on spec"

**Symptom.** An activation failed *after* `/etc` had already been switched.
The device reported the revision it had **attempted**, that matched the ring
target exactly, and the console called it on spec — while directory login,
endpoint security and secret delivery were all dead on the machine.

**Cause.** A revision says what a device meant to run. Nothing said whether
it works. The console had no health signal at all, so a matching revision was
the whole verdict.

**Fixed by** (Sextant 0.80.0, commit `2015845`): the agent asks systemd and
reports both its overall state and the names of the failed units
(`agent/src/collect.rs`). The names are the actionable half — "degraded" only
says something is wrong, `sssd.service` says where to look. A device that
reports a failure is not on spec however well its revision matches, and it is
not "applying" either: nothing is closing that gap, and telling an operator to
wait is worse than telling them nothing
(`internal/domain/observed/observed.go:58`).

Silence stays neutral. An older agent, or a probe that could not run, reports
nothing and is judged on its other signals rather than accused on a
measurement it never made. `health_state` doubles as the "this beat carried a
reading" flag, which is why the stored list only moves when it is present:
without that, one old agent's beat would clear a real list of failures and make
a broken device look healthy — the exact failure this exists to catch.

Two things the tests caught that review would not have. A nil slice is SQL
NULL against a NOT NULL column, which is every beat from a healthy device, so
that would have broken check-in for the whole fleet. And the first unit parser
took the leading status bullet as the name and then discarded the row with it,
losing `sssd.service` from a three-unit list. Mutation-checked: disabling the
veto fails two tests.

## The incident detail still spoke in release numbers

**Symptom.** Spotted on the device page during the run: *"On release 274,
target is release 275 (1 behind)"*.

**Cause.** Only the core carries a version; everything else is on spec or it
is not. That was decided in `#21` and applied to the device page and the
updates board — but the incident detail is generated in the **domain**, so
the web layer's vocabulary work never reached it.

**Fixed by** (Sextant 0.80.0, commit `3d01d6a`): the incident now says the
device is not on spec and carries both revisions, which stay where they
belong — available to whoever asks, not in the headline
(`internal/domain/incident/incident.go`). Asserted with a test, because the
wording had already been fixed twice elsewhere and survived here
(`internal/domain/incident/no_release_numbers_test.go`). Mutation-checked:
putting the release numbers back fails it.

**The lesson.** A vocabulary decision applied at one layer is not applied.
Two earlier fixes both looked complete from the web layer and both left the
domain untouched, and only a human reading the actual screen on real hardware
caught it the third time.

## The dashboard listed machines that no longer existed

**Symptom.** On the production console, 2026-08-05: the DEVICES card read
**2** and the inventory below it listed exactly two machines, while "Recent
device activity" on the same page listed **eight** — `test9`, `test10`,
`test12`, `test13`, `test14`, `test15` alongside the two real ones. The
ghosts showed a dash for their revision and "offline", so they read as
neglected fleet members rather than as records of machines that had been
removed.

**Cause.** Direction of the join. Every other surface starts at the config
plane and looks up observed state per device — the devices list and its CSV
(`internal/http/web/devices_page.go:33` walks `f.DeviceTags()`), compliance
(`internal/app/compliance.go:65` walks `f.Devices`), the policy counter
(`internal/http/web/policies_page.go:137` guards on `f.Devices[tag]`). The
overview did the opposite: it walked `Inventory.StatusAll` and admitted
anything the viewer could see, and at org scope `scopeFilter` passes
everything. Removing a device deletes its config record and deliberately
leaves its check-in history behind, so every device ever removed came back
on the dashboard and stayed there.

The online counter had the same defect one line further on: it counted
`status`, so a removed machine still checking in would have been counted
online, and the console could report more machines online than it had. That
did not show here only because these particular ghosts were all offline.

**Fixed by**: the overview drops observed rows with no
config record (`internal/http/web/overview.go`). Asserted with
`internal/http/web/overview_ghost_test.go`, which seeds two removed devices
into the observed plane — one of them freshly checked in, so the online
count is pinned too. Mutation-checked: removing the guard fails all three
assertions.

**Not changed, deliberately.** The check-in history itself stays. It is
audit material, and the console's job is to not present it as a live fleet.
`GET /api/v1/status` still returns observed rows for removed devices; that
is the observed plane's own API and raw history is a defensible answer
there, but it is worth a decision rather than an assumption.

**The lesson.** Three surfaces joined config→observed and one joined
observed→config. The odd one out was the dashboard, which is the first
screen an operator sees and the one that sets whether they believe the rest.

## Every DAWO core update failed the gate

**Symptom.** On the production console, 2026-08-05: both core updates in the
review queue sat at **Failed** with

```
gate-runner error (status 500): {"ok":false,"error":"staging candidate failed"}
```

Nothing on the page said more. The changes could not be merged, and the two
that were visible were not a coincidence — every core update fails this way.

**Cause.** The gate stages the candidate `fleet.json` as a throwaway commit in
a scratch worktree and evaluates that, because a clean tree is what keeps the
eval cache alive (a dirty flake is copied to the store whole on every eval).
The commit was made without `--allow-empty`. A **core** update moves the
flake's core pin and leaves `fleet.json` byte-identical, so the candidate
equals its base, `git commit` exits 1 with "nothing to commit, working tree
clean", staging fails, and the console reports a 500.

An unchanged candidate is a perfectly ordinary request. The gate is asked
whether a configuration evaluates, and "the same one that already evaluates"
is a fine thing to be asked.

**Fixed by**: `--allow-empty` on the staging commit
(`cmd/gate-runner/main.go`), with `cmd/gate-runner/stage_test.go` covering an
unchanged candidate, a changed one, and the reused-worktree path that
production actually runs. Mutation-checked: removing the flag fails two of the
three.

**Why it took a pod log to find, which is the more useful half.** The runner
logs the real cause and deliberately returns only a fixed string. So the
console showed `staging candidate failed` and the actual sentence — "nothing
to commit, working tree clean" — existed solely in
`kubectl logs sextant-gate`. Note the asymmetry: a *validation* verdict does
return its detail (`handleValidate` passes `err.Error()` straight through on
422). Only infrastructure failures are opaque, and those are exactly the ones
an operator cannot reason about from the outside.

This is the same defect the acceptance plan already names for imaging at
A3.8 — "the message carries the tail of the install log, not just
`nixos-anywhere failed`". The rule was written down for one surface and not
applied to the other.

**Then fixed too, after looking at what actually travels.** The first
assessment here called it a judgement call between operability and
infrastructure disclosure. Reading the code narrows it: `sync` — the call
that talks to the private overlay remote and whose git output names that
host and repository path — already has its own fixed message
(`"overlay sync failed"`). Only `stageCandidate` was being flattened, and
that is local git alone: worktree add, checkout, add, commit. Its output
carries container paths at worst, to a reader who is already logged in with
at least Viewer on a scope.

So the 500 now carries a bounded `detail` (`cmd/gate-runner/main.go`,
`shortDetail`, 500 characters, tail-trimmed because the useful sentence in a
git failure is the last one). Sync stays opaque, deliberately and with a test
that fails if it ever starts leaking. The console renders the runner's words
instead of the JSON document (`internal/adapters/gate/remote.go`), and an
older runner that sends no detail still produces a readable message.

Note the asymmetry that made the original framing wrong: the 422 validation
path already returns `err.Error()` unfiltered, and that is the class shaped by
user input — strictly more exposed than a local git failure. The caution was
being applied to the safer of the two.

## An approval queue nobody was told about

Not a defect that broke something, and worth the same weight anyway: it came
out of one question while preparing A16 — do four-eyes approvals reach a
mailbox?

**What was true.** Change approvals do: a change reaching Ready emits
`ApprovalNeeded` to the owner groups (`internal/app/change.go:334`), and every
emitted notification is also mailed, resolving the audience to addresses
through the seen-users directory (`cmd/sextant/capabilities.go:282`).

Elevation requests did not. `NewElevationService` had no notifier at all, in
the service or the domain. The only way an operator learned that somebody was
standing at a machine with a dialog open was to have `/elevation` open at that
moment — which the acceptance plan states as an instruction at A16.2 ("kijk in
de console op `/elevation`"). Against `elevation.TTL` of **five minutes**, that
is not an arrangement, it is a coincidence.

**Changed**: a raised request now emits `ElevationRequested` to the same
approver groups, in-app and by mail, carrying who, which machine, what they are
trying to do and when it expires — the three things the decision is made on,
so the message is useful without opening a page. Best-effort by construction:
a broken notification store or SMTP server cannot fail a request that somebody
is waiting on, and that is asserted rather than assumed.

**Why it belongs in an e2e findings document.** Nothing here was broken; A16
would have passed row by row with an operator who was already watching the
queue. The gap only appears when you ask what happens to the operator who is
not watching, which is the normal case and the one the test would never have
covered.

## Four things one core update taught us about the review queue

All found on the production console within an hour of deploying 0.81.0, by
approving a single core update and watching what happened. None of them was a
crash; all four made the console harder to trust than the machinery underneath
it deserves.

**The queue offered a pin that walks the fleet backwards.** Four core updates
sat in review — staged 11:35, 12:05, 12:35 and 13:05 — each carrying a Submit
button and nothing marking any of them stale. They are not alternatives: the
watcher stages one per upstream head, and each pins the core to the revision it
was staged for. Merging an older one after a newer one moves the fleet's core
backwards, or collides on the lock file. Neither is something a review queue
should offer without saying so. The watcher now retires the ones a new head
overtakes, before staging the new one, so two live core updates never coexist.
Only ids it minted itself (`core-`) are touched, and a failure to tidy never
blocks the staging.

**Nothing said which one was current.** The store lists changes in filename
order; for `core-<shortsha>` that is a hex prefix, an order that looks
deliberate and means nothing. The CR has carried `Created` and `Updated` since
it was written, and the cards showed neither. Now sorted newest-first, with the
timestamp on every card.

**A merge looked like a click that did nothing.** Merge runs through the
grace window: after three seconds it detaches and the browser is redirected
back to a board where the change still reads Ready, because it genuinely is
until the background merge finishes. Measured: staged 13:05, merged 15:19. The
redirect now names what is still running and the page polls itself until the
answer arrives — polling only while something is actually in flight, so an idle
tab left open does not re-fetch forever. This is the same lesson design 0011
wrote down for imaging ("start imaging must never look like nothing
happened"), unapplied one surface over.

**Six e-mails for one approval.** `WritePending` and `WriteApplied` are generic
progress notifications for a slow settings write, and they fire on a change
submit and again on its merge, on top of the change flow's own more specific
messages. Everything emitted was also mailed. E-mail is now limited to the
kinds that need somebody who is *not* looking at the console: a review is
waiting, a person is standing at a machine, the gate refused a write, a device
was wiped. Everything still arrives in-app, and that is asserted separately —
an in-app notification that stopped being recorded because it is not worth an
e-mail would be a worse bug than the noise it fixed.

**Note for whoever reads this next.** `ChangeMerged` and `RolloutDone` are
deliberately no longer mailed. They are milestones rather than requests, and
the operator who merged is by definition already at the console. If a fleet
ever wants them back, that is one line in `mailWorthy` — but it should be a
decision, which is why it is written here.

## An abandoned change kept its branch for nineteen days

Found while taking a baseline for A7.6 — before running the row, which is
the useful part: the acceptance plan's evidence line for "change intrekken" is
*"branch weg, geen wees in de lijst"*, and the wees was already there.

**Symptom.** The config repository held four `cr/*` branches. Three belonged to
core updates still in review. The fourth, `cr/cfg-device-dawo-inspoelstraat-10`,
belonged to a change recorded **abandoned on 2026-07-17** — with its linked
worktree still attached at `/data/overlay/.cr/...`.

**Cause.** `Abandon` has called `cleanup` since the change flow was written
(2026-07-09), so the cleanup ran on 17 July and failed. Both its errors were
discarded:

```go
_ = s.repo.RemoveWorktree(ctx, s.worktreeDir(cr.ID))
_ = s.repo.DeleteBranch(ctx, cr.Branch)
```

The two are coupled — git refuses to delete a branch still checked out in a
worktree — so a failed worktree removal guarantees a failed branch deletion,
and neither said a word. Why that first removal failed cannot be reconstructed,
and that is the defect rather than a detail of it. Two other changes abandoned
in the same period were cleaned up correctly, which is what kept this
invisible: it is intermittent, and nothing was watching.

**Why it matters beyond tidiness.** An abandoned change that still owns a
branch is a branch somebody can still merge by hand, carrying edits a reviewer
decided against.

**Fixed by**: `cleanup` logs both failures with the change and branch named,
and `Reconcile` — which already runs at every startup to align recorded status
with git — now also sweeps branches still owned by settled (abandoned or
merged) changes. The existing orphan clears on the next console restart.

**One thing the fix immediately taught us.** Adding the log lines showed
`worktree remove` also failing for changes that never had a worktree at all:
`Open` does not create one, `ensureWorktree` does lazily on first edit. A
warning there would fire on every ordinary abandon of an unedited change, and a
log that cries on the happy path teaches its readers to skip warnings — the
same failure e2e-2 recorded about a test that observed an anomaly and argued it
away. The removal is now attempted only when the directory exists, and the test
run was checked for spurious warnings rather than assumed clean.

## The gate cannot see a core update at all

**Symptom.** A DAWO core update passed the gate, was approved, merged - and the
overlay's `main` then stopped evaluating for the workplace class. Two modules
declared `dawo.printing.enable`: one in the overlay, one that the core had
gained on 2026-07-01 and that arrived with the bump. The station class was
unaffected; it does not import the profile that carries it.

**Why the gate said yes.** In `gateMode: remote` - production - the console
sends the runner exactly one thing:

```go
// Validate implements ports.Gate. It reads the candidate fleet.json the
// caller just wrote into repoDir and sends it to the runner; the runner's
// own overlay clone supplies the generator and modules.
```

The runner then syncs its clone to `origin/<branch>` and writes the candidate
`fleet.json` over it. So the evaluation is: **the candidate settings document,
against whatever `main` already contains.**

A core update does not touch `fleet.json`. It changes `flake.lock` on the
change's branch. That file never travels, so the gate evaluated the update
against the core it was replacing, found it consistent, and said yes - which
was true of the question it was asked and useless as an answer to the question
that mattered.

**Scope, which is wider than core updates.** Anything a change carries outside
`fleet.json` is invisible to the remote gate. That includes the custom overlay
editor (ADR 0014), whose whole point is committing Nix that must evaluate.

**Not fixed in this release.** The shape of the fix is clear - the validate
request should name the ref to evaluate, and the runner should check that out
instead of `origin/main`, since it already has the same remote - but changing
what the gate evaluates is not a change to make in the same hour as the
incident it explains.

**What made it visible.** Nothing in the console. The overlay was pinned to a
core from 24 June and the jump to 5 August carried five weeks of changes; the
collision only surfaced when a local `nix eval` was run against both device
classes before pushing. A green gate, a merged change and a broken `main`
coexisted quietly.

## Two smaller things the run surfaced

These were found while working on the local-admin CLI (`#50`) during the same
session, not by a plan row, but they belong to the round:

- **Registering a secret reference had no path but a browser** (0.80.0,
  commit `18499cc`). The API and `sxctl` had no way to do what the web form
  did. Added `GET`/`POST`/`DELETE /api/v1/secret-refs` and `sxctl secrets`.
- **`rekey-secrets.sh --add` could not run on a settled fleet** (0.80.0,
  commit `a5f6ba8`). An early-exit guard meant `--add` silently did nothing
  once the fleet's state hash was current — a silent no-op on the one script
  that stands between the operator and an unopenable fleet.
