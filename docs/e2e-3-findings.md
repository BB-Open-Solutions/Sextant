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

**Not fixed here**, because it is a judgement call rather than a bug: whether
the 500 should carry its cause to the console. It would have turned a
pod-log expedition into a glance, and the console already shows the operator
that a gate-runner 500 happened. Against that, the underlying error can name
internal paths, which the threat model treats as infrastructure disclosure.

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
