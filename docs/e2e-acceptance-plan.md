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

## A1. Console and access

| # | Action | Proof |
|---|---|---|
| A1.1 | Log in through Zitadel | own name on `/profile`, role visible |
| A1.2 | Open every page | 200, complete document, no empty sections |
| A1.3 | Log in as a reader (non-editor) | edit buttons absent, not merely greyed out |
| A1.4 | Group-scoped user | sees only their own group in `/devices` and `/compliance` |
| A1.5 | Log out | session gone, `/devices` redirects to login |
| A1.6 | Request `/status` and `/metrics` from outside | **OK** (10 Aug, 0.86.0). Measured from outside the cluster against `console.bb-open.com`: `/status` 404, `/metrics` 404, `/healthz` 200, `/readyz` 200. The probes answer `ok` and nothing else. `SEXTANT_METRICS_ADDR=0.0.0.0:9090` is set, which is the condition that closes them - with it empty they land on the public mux instead |
| A1.7 | Build identity in the footer and on the org page | visible once logged in |

A1.3 asks for actually looking: a button that is present but returns 403 is
a different bug from a button that is absent, and the second is the intent.

## A2. Enrolment

| # | Action | Proof |
|---|---|---|
| A2.1 | Walk the `/enroll` wizard | device appears in `/devices` with status "never seen" |
| A2.2 | Pick a hardware profile | profile on the device page, disko notes correct |
| A2.3 | Attach to the test group | `/groups` counts the device |
| A2.4 | Reuse the enrolment token | second use refused |
| A2.5 | Device with no group | falls back to org scope, no crash |

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
| A4.3 | Force comin-config-guard | stale config -> the guard restarts comin within the hour |
| A4.4 | Power the device off during a rollout | **OK** (10 Aug), and unplanned, which makes it better evidence. `e2e5` had been shut since 6 August when `rings/bb-laptops` was promoted to `a0f5236` - a promotion carrying a core bump. It was opened hours later, converged without prompting, and reported the new revision. Nothing was done to it: the catching up is what the pull model does when nobody is watching |
| A4.5 | Offer a broken config | the device refuses and stays on the old generation |

A4.5 is in here explicitly: a device that *does* activate a broken
generation is more dangerous than a device that lags behind.

## A5. Settings

| # | Action | Proof |
|---|---|---|
| A5.1 | Set an org setting | inherits to group and device |
| A5.2 | Group overrides org | the group value wins on the device |
| A5.3 | Device overrides group | the device value wins |
| A5.4 | Lock an org setting | the group can no longer weaken it |
| A5.5 | A dependent option without its enable | greyed out, with an explanation of when it lands |
| A5.6 | Change an image-time option | says it lands at imaging, not now |
| A5.7 | A list value (e.g. time servers) | editable line by line, arrives correctly |
| A5.8 | Set a value back to "inherit" | falls back, does not stick |

## A6. Policies and conditions

| # | Action | Proof |
|---|---|---|
| A6.1 | Create a policy with settings | appears in `/policies` |
| A6.2 | Assign to a group | devices in that group get the values |
| A6.3 | Lock a key in the policy | a lower scope cannot override it |
| A6.4 | **Open the settings editor on that group** | the row names the policy; locked shows as locked |
| A6.5 | Fill in compliance controls (BIO/ISO) | tags on the policy page, and back in the CSV export |
| A6.6 | Add a condition (`disk.free_percent >= 15`) | the policy accepts it |
| A6.7 | Try a broken condition | refused on save, not silently ignored |
| A6.8 | Push a device below the threshold | a finding on `/compliance`, with the measurement |
| A6.9 | A device that reports nothing | **no** finding - unmeasured is not a violation |
| A6.10 | Remove the condition again | the finding disappears |

A6.9 is the rule that carries the behaviour: a fleet that accuses machines
it cannot measure teaches operators to ignore the whole category.

## A7. Changes and the gate

| # | Action | Proof |
|---|---|---|
| A7.1 | Submit a change | appears in `/changes` with a diff |
| A7.2 | The gate runs | builds, verdict on the change |
| A7.3 | Make the gate fail | the change cannot be merged |
| A7.4 | Turn on four-eyes | you cannot approve your own change |
| A7.5 | Second approver | the merge succeeds |
| A7.6 | Withdraw a change | **OK** (5 Aug, 0.82.0). Three stale core updates rejected at 17:54:11/12/14; all three `abandoned`. Afterwards **no `cr/` branch at all** in the repo, and `git worktree list` shows only `/data/overlay [main]`. Measured from both sides: console and git |
| A7.7 | Two changes at once | the second rebases or refuses cleanly |

## A8. Rollout

| # | Action | Proof |
|---|---|---|
| A8.1 | Define rings | `/updates/rollout` shows the plan |
| A8.2 | Promote a wave | only ring 1 gets the revision |
| A8.3 | Wait out the soak | does not promote before the time is up |
| A8.4 | Health threshold not met | promotion stops |
| A8.5 | Let a wave stall | after the stall window, an incident naming the devices |
| A8.6 | `[risk:high]` in a change | extra confirmation required |
| A8.7 | Auto-flow on | promotes by itself up to the last ring |
| A8.8 | Roll a ring back (pin) | devices go back, with no handwork |

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
| A11.1 | Add a secret | encrypted in git, plaintext nowhere |
| A11.2 | A new device receives it | decrypts without a manual rekey |
| A11.3 | Run a rekey | all recipients included, old ones gone |
| A11.4 | Revoke a secret | the device can no longer read it |

## A12. Demonstrability

| # | Action | Proof |
|---|---|---|
| A12.1 | Audit log | every change with who, what, when |
| A12.2 | Evidence export | a file with the assurance configuration |
| A12.3 | Devices CSV | matches the screen |
| A12.4 | Policies CSV | controls are in it |
| A12.5 | Create and use a service account | works, appears in the audit |
| A12.6 | Fire a notification | it arrives (or mail is deliberately off - then N/A) |

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
| A14.2 | Try a reserved name | refused on save |
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
