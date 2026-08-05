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
