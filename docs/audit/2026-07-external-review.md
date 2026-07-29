# External code review, 2026-07-30 - verified

An outside reviewer read the codebase and reported five flaws. Each one was
then checked against the code. **Two hold up, one holds up in a much
narrower form than claimed, and two are wrong** - one of them because the
reviewer quoted code that is not in the repository.

Recorded in full because a review that gets things wrong is still useful:
the wrong claims say which parts of the design are easy to misread from the
outside, and that is worth knowing for a repo about to be public.

| # | Claim | Verdict |
|---|-------|---------|
| 1 | Global mutex held across nix calls serialises everything | **True, narrower scope** |
| 2 | Merge dual-write has no split-brain mitigation | **Mostly false** - gate re-check + rollback exist; a small window remains |
| 3 | Production image ships `sxctl` and `fleetsim` | **True** |
| 4 | `InsecureSkipVerify` can be set in production | **False** - unreachable from any configuration surface |
| 5 | Nix eval bomb is an effective DoS on the control plane | **False in the deployed configuration** - the eval runs in a separate, memory-capped pod |

## 1. Global mutex across nix invocations - TRUE, but narrower

Confirmed. `internal/app/change.go:216-280`: `Submit` takes `s.mu` and holds
it across `s.gate.Validate` (line 248) and `s.builder.Build` (line 250).
`Open`, `EditFile`, `Edit`, `Merge` and `Abandon` take the same mutex, so
while one change builds, no other change can be opened, edited, submitted,
merged or abandoned.

**Where the reviewer overstates it.** "No other user can ... across the
entire system" is not right:

- `Get` (:133), `List` (:141) and `Diff` (:368) take no lock, so reading the
  pipeline stays responsive.
- `ConfigService` has its own separate `writeMu`
  (`internal/app/config.go:42`), so ordinary settings writes, rollouts,
  device check-ins and every other console page are unaffected by a change
  building. The blast radius is the change-request pipeline, not the console.

**Where it is worse than stated.** The build timeout defaults to 30 minutes
(`internal/adapters/nix/build.go:16-17`), so a single heavy submit can hold
the pipeline lock for a long time. And `ConfigService.Merge` runs its own
`gate.Validate` under `WithWriteLock` (`change.go:313-341`), which means a
merge briefly blocks settings writes too - that one is deliberate and
documented (the merge mutates the same working tree).

**Mitigation, in order of cost.** (a) Cheapest and immediately useful: cap
the build timeout in the chart so the worst case is minutes, not half an
hour. (b) Correct fix: move `Validate`/`Build` out of the critical section -
the lock is needed for the git worktree operations, not for the nix call.
Take the lock to prepare the worktree, release it, run the gate against that
worktree path, re-take it to record the verdict, and re-check the branch tip
did not move. (c) Bigger: per-change worktrees plus a build queue, which also
lets the UI show "queued" honestly instead of hanging.

Do not "fix" this by removing the serialisation wholesale: concurrent nix
evaluations on one store contend anyway, and git worktrees on a single repo
are not freely concurrent.

## 2. Merge dual-write split-brain - MOSTLY FALSE

The reviewer quotes a three-line body that does not exist in the repo. The
actual `Merge` (`change.go:285-364`) does considerably more:

- runs the whole merge inside `s.cfg.WithWriteLock` so a concurrent settings
  write cannot interleave on the shared index (:313),
- captures the pre-merge tip and **re-validates the merged RESULT** through
  the gate, because two individually valid changes can merge into an invalid
  whole without a git conflict (:318-325),
- on gate failure does `ResetHard(pre)` - the merge is rolled back, and if
  the rollback itself fails the error says so explicitly (:326-330),
- persists `Merged` **immediately after** the merge, before the snapshot
  reload and before the push, with a comment stating exactly why: to keep the
  store in step with git if a later step fails (:310-312, :331-336).

So the ordering the reviewer presents as an oversight is the deliberate
choice, and it is the safer of the two orderings.

**What remains true.** If `store.Put` fails in that narrow window, git holds
the merge while the database still says `Ready`, and there is no
reconciliation loop to repair it. Same for a failed push: local git and the
database both say `Merged` while the remote does not have the commit
(:345-349 returns the error but nothing retries).

**Mitigation.** Not atomicity - git is the source of truth by design, so
reconcile from it. On startup (and on the existing sync loop), for any change
in `Ready` whose branch is an ancestor of `main`, record it as merged. For
the push: the HA sync loop already pushes; make sure a merge whose push
failed is retried there rather than only surfaced as a request error. Both
are additive and cheap.

## 3. Production image ships more than the server - TRUE

`Dockerfile:19-21` builds `sextant`, `sxctl` and `fleetsim`; `:27-31` copies
all three into the runtime stage. `fleetsim` is explicitly test tooling -
its own doc comment says "Test tooling only" (`cmd/fleetsim/main.go`) - and
it speaks the check-in API with nothing but the shared check-in token.

The Dockerfile does explain itself (`:29-30`: the demo instance runs
fleetsim as a sidecar from this same image), so this is a conscious
trade-off rather than an oversight. It is still the wrong trade-off for a
production control plane: a fleet simulator inside the container is a ready
tool for generating fake device state.

**Mitigation.** Split the runtime stage: `FROM base AS server` with only
`sextant`, and a `sim` target carrying `fleetsim` for the demo deployment to
use. `sxctl` is a judgement call - decide whether in-container
administration is wanted; if not, drop it too. Cheap, unambiguous, and worth
doing before the repo is public.

## 4. `InsecureSkipVerify` reachable from configuration - FALSE

The field exists (`internal/adapters/ldap/ldap.go:35-36`) and is read at
`:163`. It is never written. There is no `SEXTANT_LDAP_INSECURE*`
environment variable, no helm value, and no fleet setting; the only
construction site is `cmd/sextant/capabilities.go:257-258`, which sets
`URL`, `BindDN` and `BindPassword` and leaves the field at Go's zero value.
An administrator cannot set it - there is no surface to set it from.

**Mitigation anyway.** Delete the field. A knob that cannot be reached is
dead code, and dead code that disables TLS verification is an invitation to
the next person who needs to "just test something". No knob, no footgun.

## 5. Nix eval bomb as a control-plane DoS - FALSE as deployed

Two independent limits, both already in place:

- **The eval is bounded in time.** `EvalGate.Timeout` defaults to 120s per
  batch and is applied to every invocation
  (`internal/adapters/nix/gate.go:46-47, 206, 223, 256-262`).
- **The eval does not run in the control plane.** Production runs the console
  with `--gate=remote --gate-url=http://sextant-gate:8090` (verified on the
  live deployment), so `nix eval` executes in a separate `sextant-gate` pod.
  That pod is capped at cpu 2 / memory 6Gi while the console itself runs at
  cpu 1 / memory 512Mi. A runaway evaluation OOM-kills the gate runner, in
  its own cgroup, and the console keeps serving.

So the described attack costs the attacker a rejected change and the operator
a restarted sidecar - not the control plane.

**What remains true.** Two edges. First, while that eval runs, finding 1's
mutex is held, so the change pipeline is blocked for up to the timeout -
which makes finding 1 the real issue and finding 5 an amplifier of it, not a
vulnerability of its own. Second, an operator who runs `--gate=eval`
(in-process) puts the evaluation back inside the 512Mi console; the chart's
fail-safe gate already makes that combination require an explicit
acknowledgement, and it should stay that way.

**Mitigation.** Nothing urgent. If hardening further: `--option max-jobs`
and a lower per-eval memory ceiling on the runner, and design 0003
(sandboxed eval) remains the long-term home for restricting what an
evaluation may do at all.

## What the reviewer credits, and it checks out

Worth recording because these are invariants not to break:

- **The domain layer is pure.** `internal/domain` imports no I/O, no
  adapters and no application code, which is why its tests are instant.
- **Batched writes.** The Postgres adapter uses `pgx.Batch` (e.g.
  `DiscoveredStore.Report`) to put DELETE and INSERT in one round-trip
  instead of an N+1 pattern.
- **HTTP hardening.** `internal/http/mw/mw.go` enforces CSP, HSTS and
  X-Frame-Options; CSRF uses `subtle.ConstantTimeCompare`; handlers wrap
  bodies in `http.MaxBytesReader`.
- **Four-eyes is in the domain, not the UI.** `ChangeService` rejects a
  merge where author and approver are the same identity.

## What to do, ranked

1. **Cap the build timeout in the chart** (finding 1a) - one value, removes
   the 30-minute worst case today.
2. **Split the Dockerfile runtime stage** (finding 3) - mechanical, and it
   matters more once the repo is public.
3. **Delete `InsecureSkipVerify`** (finding 4) - one field.
4. **Move the gate/build call out of the pipeline mutex** (finding 1b) - the
   real fix; needs care around re-checking the branch tip afterwards.
5. **Reconcile change status from git** (finding 2) - additive, closes the
   only genuine split-brain window.

Findings 1 and 3 are the ones a reader of the public repo could reasonably
raise again, so they are the ones worth closing first.
