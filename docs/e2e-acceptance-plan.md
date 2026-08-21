# Acceptance script for e2e-3 and e2e-4 (the road to 1.0.0)

Two runs over the same console and the same device:

- **Run A - e2e-3, without integrations.** Proves Sextant stands on its own
  feet: enrol, image, converge, control, demonstrate. No NetBird, no LDAP,
  no Wazuh, no OpenBao.
- **Run B - e2e-4, with integrations.** Same device, integrations added.

The order is not optional. Run A first, because otherwise no failure in
Run B can be attributed: a device that will not converge while SSSD, NetBird
and Wazuh are all on gives you four suspects and no culprit. That is exactly
how e2e-2 lost time.

Version under test: **console 0.79.0 -> 0.80.0** (prod), overlay `bb-open`
main, core DAWO-NixOS as pinned in the ring. The run started on 0.79.0;
three findings led to fixes that shipped in 0.80.0 the same evening, so the
later rows were measured on 0.80.0. Note per row which of the two you were
looking at where it matters.

## Where the run stands

**Run A stopped sequentially at A3.11.** Session of 4 August 2026: P1
through A3.11 walked, ending with the rekey under a real admin identity.
That step unblocks A4 (convergence) and A5 (settings) - without decryptable
secrets on the machine it will not converge, exactly the failure picture
from e2e-2. **A4 through A17 are still to do.**

Two rows out of sequence are recorded: **A7.6 was verified on 5 August**
against 0.82.0, from both sides, and **A1.6 on 10 August** against 0.86.0 -
that one needs no hardware, only a request from outside the cluster.

**The plan itself was re-read against the code on 10 August**, because two
other documents turned out to be stale that week. Five checkable expectations
were verified rather than assumed: the core grace period (`incident.CoreGrace`,
14 days, A9.3/A9.4), the inactivity window (`observed.InactiveWindow`, 14
days, A9.5), the condition metric name (`disk.free_percent`, A6.6), the
provisional device state (A3.9), and the closed endpoints (A1.6). All five
hold. Unlike the station runbook and the fit-gap, this document had not
drifted.

The three findings from that session are in `docs/e2e-3-findings.md`. The
devices were called `e2e4` and `e2e5`; those are the labels the commits are
filed under.

## How to fill this in

Every row has an **action** and a **proof**. The proof is what you write
down - not "works", but what you saw. A step with no observable proof has
not been tested, even when nothing went wrong.

Record per row: `OK`, `FAIL` (plus what you saw), or `N/A` (plus why).
Findings go into `docs/e2e-3-findings.md` and `docs/e2e-4-findings.md`
respectively, in the same shape as `e2e-2-findings.md`: symptom, cause,
evidence, fix.

Two traps from earlier rounds, spelled out because both cost money:

1. **Measure one thing at a time.** In e2e-2, five fixes went in before one
   measurement; which of the five did it was never established.
2. **Do not believe the message, measure the state.** `flux reconcile`
   reports "applied" and `kubectl rollout status` reports "rolled out" while
   the old pod keeps running. Ask for the image tag, not the status.

## Preparation (once, before Run A)

| # | Action | Proof |
|---|---|---|
| P1 | Establish the console version | the footer or the org page shows the version once logged in; from outside, `/status` and `/metrics` both 404 |
| P2 | Wipe the device completely | disk wiped, firmware in setup mode if Secure Boot is in scope |
| P3 | Deliberately leave the ring pin behind main | the ring points at an older revision than main - this is the #16 condition |
| P4 | Set up an empty test group and test ring | group visible in `/groups`, ring in `/updates/rollout` |
| P5 | All integration settings off | `/integrations` shows everything off at org level and on the test group |

P3 is not a detail. Up to 0.69.0, a device imaged while its ring lagged
behind main was born on the wrong revision and could not get out of it by
itself. That is the one task still unproven on hardware, so the setup has to
force the condition rather than hope for it.

---

# Run A - without integrations

## What each remaining row needs, sorted 2026-08-11

Ninety-odd rows read as one wall of work, and they are not. Sorted by what
they physically require, most of it can be done in a browser at a desk.

**Note on A1 to A3.** They were walked on 4 August and almost none carries a
recorded proof, so by this document's own rule they are untested. They are
listed here as work rather than as history.

### Console only - a browser, no device (about 30 rows)

Can be done in one sitting, at a desk, with nothing plugged in.

| Section | Rows |
|---|---|
| A1 Console and access | 1.1, 1.2, 1.3, 1.4, 1.5, 1.7 |
| A2 Enrolment | 2.1, 2.2, 2.3, 2.4, 2.5 |
| A5 Settings | 5.5, 5.6, and the editing half of 5.7 |
| A6 Policies | 6.1, 6.3, 6.4, 6.5, 6.6, 6.7, 6.10 |
| A7 Changes and the gate | 7.1, 7.2, 7.3, 7.4, 7.5, 7.7 |
| A8 Rollout | 8.1, 8.6 |
| A12 Demonstrability | 12.1, 12.2, 12.3, 12.4, 12.5, 12.6 |
| A14 Local administrator | 14.2 |

A7 is worth doing first: it is also gate item 6, the ref-merge fix that ships
in production and whose behaviour nobody has re-measured. A change carrying a
`flake.lock` bump proves both at once.

**Walked 2026-08-21, and half of it cannot be walked at a desk.** A7.1 and
A7.7 are done against a local console. A7.2 and A7.3 need a gate, and a local
console has none: the example overlay's flake takes Sextant as `path:../..`,
which stops resolving the moment the overlay becomes a git repository, and the
console requires it to be one (issue #74). A7.4 and A7.5 need a second
identity, and `--dev-auth` mints exactly one (the same limit A12.5 records for
tokens). Those four want the real overlay, a gate-runner and an IdP - so they
belong in the "console plus a running device" sitting, not this one.

**Done for the gate half, 2026-08-18.** The overlay change
`chore/core-follows-main` carries a `flake.lock` bump (core to 0.1.2, and with
it nixpkgs 4 July to 9 August). The production gate-runner validated that ref
for `dawo-inspoelstraat` and `e2e5` and returned `ok:true`; a control run with
a ref that does not exist failed with `couldn't find remote ref`, which is what
makes the `ok` mean something. See gate item 6 in `1.0-fit-gap.md`. The console
half of A7 is still open: this went to `/validate` directly, so what is proved
is the runner, not the console screens around it.

### Console plus a running device - both ends observed (about 30 rows)

**A simulated fleet counts for a good deal of this.** `just demo` runs devices
that check in, converge onto their ring branch, report usage and health, and
can be given an error on demand - which is what the console sees of a real
device. A8.2 to A8.4 were walked that way on 2026-08-21 and found two things
no amount of reading would have.

What it does NOT prove: anything the device itself does. A simulated device
never evaluates nix, never switches a generation, never fails an activation for
a real reason. So the wire contract and every console-side judgement are
testable at a desk; the device's own behaviour is not.

Needs a device online and someone watching the console. The device does not
need hands on it.

A6.2, A6.8, A6.9 · A8.2 to A8.5, A8.7, A8.8 · all of A9 · A10.1, A10.2, A10.3
· all of A11 · A13.1, A13.2, A13.2b · A14.1, A14.3 · A16.2, A16.5, A16.7 ·
A17.1 to A17.5, A17.8

### Hands on a machine (about 30 rows)

Somebody sitting at the device: plugging things in, watching a dialog, timing
a build.

| Section | Why |
|---|---|
| A3 Imaging, all of it | a machine to image, and one to fail deliberately |
| A10.4, A10.5 | a wipe, and a wipe that does not finish |
| A13.3 to A13.6 | USB devices plugged in; a printer |
| A15, all twelve | a desktop session and its dialogs |
| A16.1, A16.3, A16.4, A16.6 | the elevation dialog on the laptop |
| A17.6, A17.7 | substitution timed, and a credential broken on purpose |

### What this changes about the order

The console block is the largest single thing that needs no hardware and no
travel, and it contains the gate proof that 1.0 is waiting on. A15 is the
biggest hands-on block at twelve rows and needs one uninterrupted session at a
laptop rather than being picked at.

## A1. Console and access

| # | Action | Proof |
|---|---|---|
| A1.1 | Log in through Zitadel | **OK** (18 Aug, 0.87.0). `/profile` shows `b.buijs@bb-open.com`, IDP group `dawo-beheer`, and an effective-roles table: Owner at `org` and at each of the five groups. The role is named, not implied |
| A1.2 | Open every page | **OK** (18 Aug, 0.87.0). Every entry in the navigation returns a complete document: Overview, Devices, Groups, Enrollment, Settings, Policies, Compliance, Requests, Integrations, Updates, plus `/changes`, `/org` and `/profile`. Empty states are written rather than blank ("Nobody is waiting", "No policies defined", "Select a group from the hierarchy"). One note for whoever walks this next: the Requests item points at `/elevation`, so a hand-typed `/requests` 404s and that is the URL being wrong, not the page |
| A1.3 | Log in as a reader (non-editor) | edit buttons absent, not merely greyed out. **Not tested:** needs a second account that is not an Owner, and this deployment's only human account holds Owner at org |
| A1.4 | Group-scoped user | sees only their own group in `/devices` and `/compliance`. **Not tested:** same reason as A1.3 |
| A1.5 | Log out | session gone, `/devices` redirects to login |
| A1.6 | Request `/status` and `/metrics` from outside | **OK** (10 Aug, 0.86.0). Measured from outside the cluster against `console.bb-open.com`: `/status` 404, `/metrics` 404, `/healthz` 200, `/readyz` 200. The probes answer `ok` and nothing else. `SEXTANT_METRICS_ADDR=0.0.0.0:9090` is set, which is the condition that closes them - with it empty they land on the public mux instead |
| A1.7 | Build identity in the footer and on the org page | **FAIL** (18 Aug, 0.87.0). Neither carries one: the footer is the tagline plus an audit-log link, and `/org` ends in presentation defaults. The one place that does carry a version is wrong - the running pod reports `sextant_build_info{version="dev"}` while the deployment is `sextant:0.87.0`. `cmd/sextant/main.go:36` declares `var version = "dev"` and nothing sets it: `flake.nix` builds with `ldflags = [ "-s" "-w" ]`. Filed as issue #42. This also blocks P1, which tells you to establish the console version from exactly these two places |

A1.3 asks for actually looking: a button that is present but returns 403 is
a different bug from a button that is absent, and the second is the intent.

## A2. Enrolment

**A station has to be registered before the console will show what it found.**
Measured 2026-08-21: the simulated station's reports were accepted (`station
report accepted station=st-1 devices=4`) and its machines stored, while
`/enroll?station=st-1` answered *unknown station* and `/station` said *No
stations registered yet*. Registering the tag made all four appear at once, so
nothing was lost - it was invisible. Register first, then walk this section.

| # | Action | Proof |
|---|---|---|
| A2.1 | Walk the `/enroll` wizard | **OK** (21 Aug, chart 0.90.0). A machine picked from the station's PXE list, given a name, class and group, appears in `/devices` as `never seen`. **First register the station**: the console refuses `/enroll?station=X` with *unknown station* until the tag is registered on `/station`, even though its reports are already being accepted and stored - see the note under this table |
| A2.2 | Pick a hardware profile | **OK after a fix** (21 Aug). The profile name reaches the device page. The disko note and the imaging steps did **not** reach any page at all: `Disko` existed only as a struct field and `Steps` was carried into the enrolment row's view model and never rendered, so the page promising "brand-specific guidance" showed none. Both now render, on the Hardware page and beside the machine on the enrolment list |
| A2.3 | Attach to the test group | **OK** (21 Aug). Enrolled into `ict-test` from the form; the device carries the group on `/devices` and the group tree lists it |
| A2.4 | Reuse the enrolment token | **not walked** (21 Aug) - the one-time credential a station receives with a claimed job needs the station path, not the enrolment form. What the form did show: enrolling the same machine twice creates **two** device records, the second with no serial, no model and no spec, and nothing on screen says the machine already has one. Filed as issue #79 |
| A2.5 | Device with no group | **OK** (21 Aug). Enrolled with an empty group: the device page renders and `/settings?scope=device:<tag>` renders, both 200, resolving against the organisation |

## A3. Imaging (station)

| # | Action | Proof |
|---|---|---|
| A3.1 | Boot the station, register the device | station visible in `/station`, job claimable |
| A3.2 | Start an imaging job | job reaches status "claimed" |
| A3.3 | **Check the revision in the job** | the job carries the **ring pin**, not main - this is #16 |
| A3.3b | Rev in the station's claim response | `rev` is present and equals the ring pin - the place it fell away until 0.74.0 |
| A3.4 | Finish the install | device boots, no manual step |
| A3.5 | First check-in | device reports with the ring revision |
| A3.6 | Host key as an age recipient | secrets decrypt on the device with no handwork |
| A3.7 | Quiet boot | no kernel debug over the console |
| A3.8 | Deliberately fail an install | the message in the console carries the **tail of the install log**, not just "nixos-anywhere failed" |
| A3.9 | After A3.8: no ghost device | the failed attempt stands as **Provisional** and does not count toward `RingStatus.Total`; a following rollout proceeds normally |
| A3.10 | Re-image the same machine | updates the existing registration (same chassis serial), does not mint a second |
| A3.11 | **Rekey under a real admin identity** | as soon as `/api/v1/hostkeys` is no longer empty: `scripts/rekey-secrets.sh -i ~/.ssh/bbuijs -s ../bb-open/secrets`, after which `~/.ssh/bbuijs` opens the secrets. This is the step that was missing on 31 July and nearly made the fleet unopenable - it is only possible here, with a freshly reported host key |

A3.3 is the heart of the evening. Look inside the job itself, not at the end
result: if the device happened to be on main already, the fix is not proven.

## A4. Convergence

| # | Action | Proof |
|---|---|---|
| A4.1 | Change a setting, merge | **OK** (10 Aug, overlay `bd0b1b6`). Three identity settings changed in `fleet.json`, promoted to `rings/bb-laptops` as a single commit. `e2e5` converged on its own poll and reported the new revision to the console. The rendered result was checked on the machine, not only the revision string: `sssd.conf` went from `ldap://10.43.76.5` to `ldaps://10.43.76.5:636` |
| A4.2 | comin status | **OK** (11 Aug). `comin` active on both devices, following `rings/infra` and `rings/bb-laptops` respectively, and its own store records the last deployment as `status: done` with an empty `error_msg`. `dawo-comin-config-guard` is a timer that fires hourly and has produced nothing but its own start and stop lines for seven days - it has never had to intervene, which is the half of this row that is easy to skip |
| A4.3 | Force comin-config-guard | **OK** (11 Aug, on `dawo-inspoelstraat`). comin was pinned to a superseded config path with a runtime drop-in while the generation named the current one. The guard detected it and logged *"comin is running with a stale configuration; restarting so it follows the branch this generation assigns"*, and comin's `ActiveEnterTimestamp` equals the guard's exit timestamp to the second - so it restarted the process rather than only complaining. It stayed stale afterwards because the drop-in was still in place, which shows the guard re-measures each run instead of remembering a verdict. **A first attempt measured the wrong thing**: changing a comin setting does not leave a stale process, because comin restarts itself (`restart_comin=True` in its own store). The two mechanisms cover disjoint cases - comin's own restart fires when it *performs* a deployment that changes its config; the guard covers a comin that performs none at all, which is the test15 failure it was written for |
| A4.4 | Power the device off during a rollout | **OK** (10 Aug), and unplanned, which makes it better evidence. `e2e5` had been shut since 6 August when `rings/bb-laptops` was promoted to `a0f5236` - a promotion carrying a core bump. It was opened hours later, converged without prompting, and reported the new revision. Nothing was done to it: the catching up is what the pull model does when nobody is watching |
| A4.5 | Offer a broken config | **OK** (11 Aug, on `dawo-inspoelstraat`). A module that cannot evaluate was pushed to `rings/infra` and to no other branch. The station kept generation `0373qpsw` unchanged and logged the assertion by name on every poll; nothing was built, because nothing evaluated. **The row also exposed something it was not asking about**: the console showed the device with no error at all while it failed to converge every two minutes. comin records only deployments it actually starts, so a failed *evaluation* leaves no entry - and the agent's failure reporting, added the day before, reads exactly that entry. The real signal is comin's own exporter on `:4243`, which separates `comin_last_fetch_failed`, `comin_last_eval_failed`, `comin_last_build_failed` and `comin_last_deployment_failed`. Four failure modes; the agent covered one |

A4.5 is in here explicitly: a device that *does* activate a broken
generation is more dangerous than a device that lags behind.

## A5. Settings

| # | Action | Proof |
|---|---|---|
| A5.1 | Set an org setting | **OK** (11 Aug). `autoUpdate.options.pollSeconds` is 300 at org, unset for `bb-laptops` and unset on `e2e5`; the generator gives that device 300. Proved through the nix generator on a real host rather than through the console resolver, because the generator is what the device actually gets |
| A5.2 | Group overrides org | **OK** (11 Aug), and it needed no change to prove: org 300, group `infra` 120, no device value, and `dawo-inspoelstraat` evaluates to 120 |
| A5.3 | Device overrides group | **OK** (11 Aug). 45 set on the device against the group's 120; the station evaluates to 45 |
| A5.4 | Lock an org setting | **OK** (11 Aug). With `autoUpdate.options.pollSeconds` in the org's `enforced` list, the station evaluates to the org's 300 even though its group sets 120. Removing the lock returns it to 120, so the lock is what changed and not the order of anything else |
| A5.5 | A dependent option without its enable | **OK** (18 Aug, 0.87.0), console half now closed. With `desktop.plasma.enable` off, "Chat client" renders dimmed and carries a broken-link marker with the sentence "Takes effect once `desktop.plasma.enable` is on - saving a value now is fine, it stays staged." Both halves of the row: it greys out AND it says when the value lands. Worth recording the nuance, because it looked like a failure at first glance: a dependent field that already holds a value stays fully editable and is marked "Modified", as `autoUpdate.options.pollSeconds` is at org. The dimming marks "nothing here yet", not "you may not type here" |
| A5.6 | Change an image-time option | **OK** (18 Aug, 0.87.0), console half now closed. Both DiskUnlock keys carry "Image-time setting: takes effect when a device is (re)imaged, not on running devices" under the description, and the LUKS mapper field shows it alongside its takes-effect-once line, so the two kinds of "not yet" are distinguishable in one glance |
| A5.7 | **OK** (21 Aug, chart 0.90.0), both halves. Arrival was proved 11 Aug: three NTP servers on the `infra` group arrive at `services.chrony.servers` as three separate values, no `["[a b]"]` concatenation (audit finding L2). The console's line-by-line editing half is now walked too, and it **failed first**: three servers saved at org landed correctly in `fleet.json` and came back as an EMPTY textarea whose border still said "set here", so the next save of that box would have cleared them. `valueLines` handled `[]any` (a re-read of the document) and not `[]string` (what a save produces), which is why it looked right again after a restart. Fixed and re-walked: the three lines come back |
| A5.8 | Set a value back to "inherit" | **OK** (11 Aug). Removing the device value put the station back on its group's 120 in the same evaluation - nothing cached, nothing stuck |

## A6. Policies and conditions

| # | Action | Proof |
|---|---|---|
| A6.1 | Create a policy with settings | **OK** (21 Aug, chart 0.90.0). `workplace-baseline` created from `/policies` with two settings, a description and two controls; it appears on the page with all of them and in `policies.csv` |
| A6.2 | Assign to a group | **OK** (11 Aug). A policy carrying two NTP servers, assigned to `group:infra`, arrives at `services.chrony.servers` on `dawo-inspoelstraat` and does **not** reach `e2e5`, which is in another group. The negative half is the half worth having: a policy that lands everywhere would pass a test that only looks at the intended device |
| A6.3 | Lock a key in the policy | **OK** (11 Aug), with its control case. With `timesync.options.servers` in the policy's `enforced` list, a device-level value of `eigen.server.local` loses and the policy's servers stand. Removing only the lock lets the device value win, so the lock is what decided it and not the order of anything else |
| A6.4 | **Open the settings editor on that group** | **OK** (21 Aug). With the policy assigned to `group:kantoor-a`, the `desktop` row in `/settings?scope=group:kantoor-a` reads **Set by policy workplace-baseline**; adding the key to the policy's `enforced` list changes the same row to **Locked by policy workplace-baseline** with a lock glyph. Note the editor does not show policy-only keys such as `diskEncryption` and `usbDevices.*` - that is ADR 0017 and deliberate, so pick a key the editor renders when walking this row |
| A6.5 | Fill in compliance controls (BIO/ISO) | **OK** (21 Aug). `BIO 12.3.1` and `ISO 27002 8.24` show on `/policies` and come back semicolon-separated in the `controls` column of `policies.csv` |
| A6.6 | Add a condition (`disk.free_percent >= 15`) | **OK via the API** (21 Aug), and **not possible from the console**: there is no field for a condition anywhere in the settings or policy editor, and no handler parses one. `PUT /api/v1/policies/{id}` with a `conditions` array applies. Conditions are a documented feature (ADR 0017) with no console surface |
| A6.7 | Try a broken condition | **half OK, and the other half was a defect** (21 Aug). An unknown operator is refused: `condition on "disk.free_percent" has unknown operator "maybe" (use >=, <=, >, < or ==)`. An unknown **metric** was accepted - and a metric the observed plane never supplies can never hold, so the policy looked like governance and did nothing. Fixed: the metric vocabulary is now closed the way the filter attributes already were, with a test pinning it against what `observed.Usage.Metrics` produces |
| A6.8 | Push a device below the threshold | **OK** (21 Aug). A check-in for `kantoor-a-001` reporting 480 of 500 GB used raised a warning on `/compliance` carrying the measurement: *Less than 15% free; a nixos-rebuild needs room (disk.free_percent is 4, and this policy requires >= 15.)* plus the line that it is a condition and converging cannot fix it |
| A6.9 | A device that reports nothing | **OK** (21 Aug). A device enrolled into the same group that has never checked in raises *has never checked in* and **no** condition finding: `does not meet Disk headroom` names only the device that was measured. Unmeasured is reported as unmeasured, not as a violation |
| A6.10 | Remove the condition again | **OK** (21 Aug). Re-applying the policy without its `conditions` array cleared the finding on the next read of `/compliance` |

A6.9 is the rule that carries the behaviour: a fleet that accuses machines
it cannot measure teaches operators to ignore the whole category.

## A7. Changes and the gate

| # | Action | Proof |
|---|---|---|
| A7.1 | Submit a change | **OK** (21 Aug, chart 0.90.0). The opening half is now walked too: with `requireChangeRequest` on, saving one setting in the editor opened `cfg-org-1` as a Draft authored by the operator, with a real `cr/cfg-org-1` branch in the overlay. Read from both sides - the console's diff view and `git diff main cr/cfg-org-1` show the same `org.settings.desktop` addition. Submitting moved it to Ready, and merging it (behind a confirmation step) put it on `main` |
| A7.2 | The gate runs | **FAIL** (18 Aug, 0.87.0), and the failure is a diagnosis defect rather than a broken gate. Pressing Build on the staged `core-06b0d7df76c8` returns `gate-runner error (status 500): staging candidate failed: fetch cr/core-06b0d7df76c8 ... couldn't find remote ref`. The change carries no commits, so no branch was ever pushed: the diff view says "No changes on this branch yet" and `git ls-remote <overlay> 'refs/heads/cr/*'` returns nothing at all. Seven changes in this queue died with that same message since 30 July. The gate itself is healthy - `/validate` returns `ok:true` on a ref that does exist, and `/bump` returns a full lock. Filed as issue #41 |
| A7.3 | Make the gate fail | **not walkable locally** (21 Aug). `just demo` runs `--gate none`, and it has to: the example overlay's flake takes Sextant as `path:../..`, which stops resolving the moment the overlay is a git repository - which the console requires it to be. So a local console has no gate to fail. Needs the real overlay against a gate-runner. Filed as issue #74 |
| A7.4 | Turn on four-eyes | **not walkable on a dev-auth console** (21 Aug). `--dev-auth` mints one synthetic owner, so there is no second identity to refuse. Needs a console behind an IdP with two accounts |
| A7.5 | Second approver | **not walkable on a dev-auth console** (21 Aug), same reason as A7.4 |
| A7.6 | Withdraw a change | **OK** (5 Aug, 0.82.0). Three stale core updates rejected at 17:54:11/12/14; all three `abandoned`. Afterwards **no `cr/` branch at all** in the repo, and `git worktree list` shows only `/data/overlay [main]`. Measured from both sides: console and git |
| A7.7 | Two changes at once | **OK** (21 Aug). Two changes opened from the settings editor, both touching `fleet.json`, both Ready. The first merged; the second was refused with `409 Another change landed first. Reload and try again.` and `main` kept only the first - a clean refusal, nothing silently overwritten. **But the advice is wrong**: merging again repeats the 409 and re-submitting gives `400 cannot move change from ready to building`, so the only way out is Abandon and redo. The row passes; the sentence after it is filed as issue #75 |

## A8. Rollout

| # | Action | Proof |
|---|---|---|
| A8.1 | Define rings | **OK** (18 Aug, 0.87.0). `/updates/rollout` names both waves, Testtoestel and Inspoelstraat, each with its gates (soak 0 min, min healthy 95%) and its current state. It also showed something the row does not ask for, recorded below |
| A8.2 | Promote a wave | **OK** (21 Aug, chart 0.90.0, against the simulated fleet). Starting a run moved `rings/ict-test` to the release and left `rings/balie`, `depot`, `kantoor-a`, `kantoor-b` and `zaanstad` on the previous revision. Measured from git, not from the board. Note the target is not `main`: the engine's own pin commit advances main, so compare against the run's target revision |
| A8.3 | Wait out the soak | **OK** (21 Aug). Wave 1 converged, entered Soaking with a 10-minute gate, and wave 2 stayed on the old revision for nine minutes of polling. It promoted after the soak expired, not before |
| A8.4 | Health threshold not met | **OK** (21 Aug). A device in wave 2 reporting the target revision WITH an error (`activation failed: sssd.service entered failed state`) took the wave to **Halted here**, and `rings/kantoor-b` never moved. Walked with a device added after the simulator started, so its beat could not overwrite the error |
| A8.5 | Let a wave stall | after the stall window, an incident naming the devices |
| A8.6 | `[risk:high]` in a change | **half OK** (21 Aug). The marker survives into the commit subject where the brake reads it: `settings: update 1 at org [risk:high]`. A rollout does show a confirmation page - target, scope, wave plan, and a warning when the plan has no gated test wave - but it is the same page for every rollout and says nothing about the marked commit in the range. That matches ADR 0012, where the brake holds the AUTOMATIC flow; it is still a gap on the screen where it matters most. Filed as issue #81 |
| A8.7 | Auto-flow on | promotes by itself up to the last ring |
| A8.8 | Roll a ring back (pin) | **FAIL** (21 Aug), and it is an absence rather than a bug. Nothing can pin a ring to a chosen revision: the engine pins forward (`SetGroupPin(g, st.Target)`), the console only **un**pins (`SetGroupPin(name, "")`), and `PATCH /api/v1/groups/{name}` refuses the field. Unpinning is not a rollback - a group with no pin follows `main`, which is ahead of the release that just broke, so the one control an operator has moves the devices forward. Filed as issue #84 with three ways to settle it |

**The rollout has been halted since 5 August, and the row above is how it was
found.** `/updates/rollout` reports:

```
Halted by a check - 79989d902391
Delivering: Core update 41d6ad18ac99
release build failed: The option `dawo.printing.enable' in `<store>' is already
declared in `<store>' via option flake.modules.nixos.services-printing.
this target is behind main by releases: 13
```

That is the option collision between the overlay's own printing module and the
core's, which is exactly what the branch pin existed to avoid and what core
0.1.2 ends. It explains the two standing compliance findings ("running an older
DAWO core") and the ring-catch-up warning without either of them naming a
cause. Thirteen releases behind is the cost of a halt nobody was alerted about.

The current configuration (`4eec77d`) evaluates for both device classes, so a
rollout started from it should walk past this. That is a fleet decision rather
than an acceptance row: the first promotion after the repin carries five weeks
of nixpkgs.

## A9. Update board and incidents

| # | Action | Proof |
|---|---|---|
| A9.1 | Device on the ring revision | shows as "matches" - no revision hashes |
| A9.2 | Config lag | **warning**, not an issue |
| A9.3 | Core lag within the grace period | warning |
| A9.4 | Core lag beyond 14 days | **issue** |
| A9.5 | Device offline > 2 weeks | incident |
| A9.6 | Device that never reported | incident |
| A9.7 | Device with a build error | incident carrying the error message |
| A9.8 | Unknown revision | incident, and not confused with "not counted yet" |
| A9.9 | Freshly imaged device | **no** report of an out-of-band change |

A9.2 against A9.4 is the split you asked for: a config that lags is a
warning, a system that lags becomes a real problem in time.

## A10. Remote actions

| # | Action | Proof |
|---|---|---|
| A10.1 | Request diagnostics | report comes back on the device page |
| A10.2 | Reveal a recovery key | the reveal is in the audit, the key is right |
| A10.3 | Set a wipe intent on an unarmed device | the device refuses, the console reports "refused" |
| A10.4 | Arm and execute a wipe | the device confirms, the disk key is destroyed |
| A10.5 | A wipe that does not complete | incident "wipe not completed" |

A10.3 and A10.5 are the most important of this group: a wipe that fails
silently is the worst thing this product can do.

## A11. Secrets

| # | Action | Proof |
|---|---|---|
| A11.1 | Add a secret | **OK** (11 Aug), checked three ways rather than by looking at one file. Every `secrets/*.age` carries an age header, so all five are ciphertext. All five secret-typed catalog options used in the fleet hold a **name** from `secretRefs` and never a value - which is the product's actual claim. And the history was searched for plaintext: the only hit is `nix/test-fixtures/ssh_host_ed25519_key`, a throwaway host key `docs/testing.md` describes as public by design, no longer in the working tree. A fourth check was mine and was wrong: comparing 24 base64 characters of a public key matches every ed25519 key ever made |
| A11.2 | A new device receives it | decrypts without a manual rekey |
| A11.3 | Run a rekey | all recipients included, old ones gone |
| A11.4 | Revoke a secret | the device can no longer read it |

## A12. Demonstrability

| # | Action | Proof |
|---|---|---|
| A12.1 | Audit log | **OK** (18 Aug, 0.87.0). `/audit` lists 100 entries, each with when, who (display name plus address), what (the commit subject) and the commit hash. The actors are kept apart rather than flattened into one operator: people by name, and `sextant-api`, `sextant-rollout`, `sextant-upstream`, `sextant-agent`, `sextant-station` for the machine paths. The evidence-export panel sits under it |
| A12.2 | Evidence export | **OK** (21 Aug, chart 0.90.0, walked against a `just demo` console). `GET /audit/evidence?from&to` returns `application/json` as an attachment named for the period, carrying the controls in force, every commit in the window with author, address, time, subject and hash, plus the change requests and the ring promotions. It exported ONE of the four assurance controls when first walked; all four now, and all four when false, so a reader can tell "not in force" from "this export does not know about it" |
| A12.3 | Devices CSV | **OK** (21 Aug). 38 rows against 38 devices on `/devices`, same columns as the screen: tag, class, hardware, assigned user, groups, online, revision, baseline, failing criteria |
| A12.4 | Policies CSV | **OK** (21 Aug). A policy created with `BIO 12.3.1, ISO 27002 8.24` exports them semicolon-separated in the `controls` column, beside the settings, the locked keys, the target, the filter and the devices reached. Walked with a policy deliberately created first: the export is header-only on a fleet without policies, which proves nothing |
| A12.5 | Create and use a service account | **OK** (21 Aug), with one thing worth knowing. A token minted for an account **in a group that has a role binding** reads the API (200 on `/api/v1/devices` and `/api/v1/me`); a viewer ceiling refuses a write with `requires owner at org (you hold viewer)`; a revoked token, a forged one and no token all give 401. An account in NO group gets 403, and the page says so plainly - the ceiling caps a role, it does not grant one. **Not testable on a `--dev-auth` console**: there a forged or revoked bearer falls through to the synthetic session and reads 200, so token behaviour has to be walked on a console without it |
| A12.6 | Fire a notification | **OK in-app** (21 Aug); mail N/A, SMTP deliberately unconfigured on the demo cell. A failed submit raised a bell entry within seconds. The body was wrong - it blamed the nix gate for a change that never reached the gate - and that is fixed in `09084e6` |

## A13. USB control and printing

| # | Action | Proof |
|---|---|---|
| A13.1 | Fill the allowlist **through a policy** | rules land in the config; the settings editor no longer offers the key |
| A13.2 | Turn on USB control in the same policy | whatever is plugged in at boot keeps working |
| A13.2b | Try to set the key through settings anyway | refused (403) - hiding is not enforcement |
| A13.3 | Plug in an allowed device | works |
| A13.4 | Plug in a device that is not allowed | blocked |
| A13.5 | Leave the keyboard out of the allowlist | **thought through in advance**: this locks you out - only test with a second way in |
| A13.6 | Turn on printing | printer found, test page comes out |

A13.5 is deliberately a warning rather than a step. The option is
`riskClass: high` precisely because an allowlist that misses the keyboard
cannot be repaired remotely.

## A14. Local administrator account

| # | Action | Proof |
|---|---|---|
| A14.1 | Turn on with a name plus a secret | local login succeeds |
| A14.2 | Try a reserved name | **not walkable locally** (21 Aug). The `localAdmin.*` keys come from the overlay's catalog and the example overlay publishes none, so there is no field to type a reserved name into. The refusal would come from the overlay's own assertion through the nix gate, which a local console does not have (issue #74). Needs the real overlay and a gate-runner |
| A14.3 | Turn off | account locked, login no longer possible |

## A15. User rights

Log in as **`bbuijs` (a directory user)**, not as the local administrator.
That is the whole point: this was found because an LDAP user is in no local
group at all, and as the local administrator you notice nothing.

| # | Action | Proof |
|---|---|---|
| A15.1 | Turn wifi on and off | happens directly, no dialog |
| A15.2 | Pick a network from the list | connects, no dialog |
| A15.3 | **Save a network for all users** | asks for **your own** password, not for an administrator password |
| A15.4 | A second privileged action right away | does not ask again (it remembers briefly) |
| A15.5 | Change the timezone | happens directly |
| A15.6 | **Set the clock by hand** | **refused** - time comes from the fleet |
| A15.7 | **Change the hostname** | **refused** - that belongs to the fleet |
| A15.8 | Mount a USB stick | happens directly |
| A15.9 | Register a dock (touches #18) | happens directly, no administrator needed |
| A15.10 | **Open user management (add an account)** | **refused** - never grantable |
| A15.11 | Log in over SSH and have `nmcli` save a network | **refused** - no seat, so no right |
| A15.12 | Add a printer | **refused** (printing is off on this group; the right is deliberately not granted) |

A15.11 is the most important of the series. `session` means "who is
physically at the machine", not "who has a shell". If this one succeeds, the
`subject.local && subject.active` clause is broken and every other rule here
is worthless too.

A15.6, A15.7 and A15.10 are negative tests. A result of "happens directly"
is a **failure** there, not a success - easy to tick off wrongly when you
race through the list.

If something behaves other than expected, watch along on the machine while
you retry:

```
journalctl -f -u polkit
```

That names the action id being refused, so we do not have to guess which
right is missing.

## A16. Elevation requests

Log in as **`bbuijs`**. Turn on a right that is `off` (for example
`firmware`) as a test target, or use an action that still asks for an
administrator.

| # | Action | Proof |
|---|---|---|
| A16.1 | Perform an action that asks for an administrator | the dialog appears |
| A16.2 | Look at `/elevation` in the console | the request is there, with user, machine and waiting time |
| A16.3 | Approve | the dialog on the laptop proceeds, **without** you typing a password |
| A16.4 | Second request, now **deny** | the dialog falls back to the password field |
| A16.5 | Third request, do nothing | expires after five minutes; disappears from the queue |
| A16.6 | Make a request with the console unreachable | falls back to the password path; the dialog does **not** hang |
| A16.7 | A reported action on the card | present with the label "reported" - context, not proof |

A16.6 is the most important. The whole construction is `sufficient` and
additive: if it fails, the dialog should behave the way it did before this
feature existed. If it hangs, that is worse than a refusal - then you cannot
even type a password any more.

A16.4 must **also** fall through to the password field. A refusal by the
operator does not lock the user out; it only says no approval is coming by
this route.

## A17. Release cache behind a credential

The order is binding. The netrc has to be on the machine **before** the
server demands the token, because a machine that cannot authenticate does
not fail loudly - it starts building its own closure. That is hours, and it
looks like a slow rollout rather than an access problem.

| # | Action | Proof |
|---|---|---|
| A17.1 | `cacheAuth.enable` on, converge | `/run/sextant/netrc` exists (mode 0600) and `nix.conf` names `netrc-file` |
| A17.2 | Contents of the netrc | password = the device credential; no new secret was rolled out |
| A17.3 | Request the cache anonymously from outside | **401** |
| A17.4 | Request the cache with the device credential | 200 |
| A17.5 | Revoke the device in the console, try again | **401** within five minutes - this is what a shared token cannot do |
| A17.6 | Roll out a wave | the device **substitutes**, does not build - look at the duration, not the end result |
| A17.7 | Deliberately corrupt the credential on the machine | the device falls back to building itself; proof that the failure mode is slow and not loud |
| A17.8 | Make the console temporarily unreachable for the gate | the cache refuses (**fails closed**), the device builds itself - not: the cache opens up |

A17.6 is the row that counts. A successful rollout proves nothing: a machine
that compiles its whole system itself also arrives. Measure the time, or
look in `journalctl -u nix-daemon` for whether anything was substituted.

A17.7 you do deliberately once, so you recognise the failure picture when it
happens to you by accident later.

---

# Run B - with integrations

Turn them on **one at a time**, with a check-in in between. Turning
everything on at once is how e2e-2 got four failures at once and missed
three of them.

## B1. NetBird (first - the rest runs over it)

| # | Action | Proof |
|---|---|---|
| B1.1 | `netbird.enable` plus a setup key on the group | the device appears as a peer in the dashboard |
| B1.2 | Route to the cluster services | the device reaches the internal address |
| B1.3 | Restart | the peer comes back by itself |
| B1.4 | The console stays reachable | check-ins continue |

## B2. Identity (LDAP/SSSD)

| # | Action | Proof |
|---|---|---|
| B2.1 | `identity.enable` with LDAPS | `getent passwd <user>` finds the user |
| B2.2 | Log in as an LDAP user | a session on the device |
| B2.3 | Home directory | gets created |
| B2.4 | Present a wrong certificate | connection **refused** - the strict path must be strict |
| B2.5 | LDAP temporarily unreachable | offline login works within its validity period |
| B2.6 | Change settings | nsncd refreshes; no old username lingers |

B2.4 and B2.6 both come out of e2e-2. SSSD turns on `ldap_id_use_start_tls`
by default and nscd/nsncd held on to old answers - two silent layers that
looked like a working setup.

## B3. Wazuh

| # | Action | Proof |
|---|---|---|
| B3.1 | `wazuh.enable` on the group | the agent registers, the manager shows it **Active** |
| B3.2 | Agent queue | survives a restart |
| B3.3 | Fire an event | it arrives at the manager |
| B3.4 | Manager away temporarily | the agent resumes by itself |

B3.2 is in here because exactly that broke in e2e-2: systemd reset the
permissions of the state directory before *every* start, while the binaries
drop down to the `wazuh` user.

## B4. OpenBao

| # | Action | Proof |
|---|---|---|
| B4.1 | Enable | the device fetches its material |
| B4.2 | Revoke the token | access stops |

## B5. Mail

| # | Action | Proof |
|---|---|---|
| B5.1 | Fill in SMTP | a test message arrives |
| B5.2 | Notification on an incident | mail with usable content |

## B6. Everything at once

| # | Action | Proof |
|---|---|---|
| B6.1 | Re-image a fully loaded device | comes up with every integration, no handwork |
| B6.2 | Full rollout across the rings | no integration breaks on a generation switch |
| B6.3 | Compliance picture | clean, or only findings you expect |

B6.1 is the actual product question: "a municipality buys the bundle" means
an empty laptop is connected to everything after one imaging action.

---

## Wrapping up

| # | Action |
|---|---|
| C1 | Write up the findings per run |
| C2 | Every FAIL becomes a task, with symptom and evidence |
| C3 | Mark what blocks 1.0.0 |
| C4 | Update this script wherever a step turned out to be unclear |

C4 is not a formality: every step where you had to think about what "good"
means is a step that will be executed wrongly next time.
