# External code review, 2026-07-30

An outside reviewer read the codebase and reported the findings below. They
are recorded here **verbatim in substance and unverified in fact**: nothing
in this document has been confirmed against the code yet, and a review's
confidence is not evidence. Task #10 tracks working through them.

Read it in that spirit. A claim like "this mutex serialises the whole
service" is either true and important or false and expensive to act on;
the point of writing it down is to check it, not to schedule a fix.

## Claimed flaws

### 1. Global mutex held across nix invocations (reviewer's most severe)

`internal/app/change.go`: `ChangeService` serialises all git branch and
worktree operations with one service-wide `sync.Mutex`, and holds it across
the synchronous `gate.Validate` and `builder.Build` calls, which shell out
to nix and can take seconds to minutes.

> Because this is a global lock across the service, while one change is
> building, no other user can open, edit, submit, merge, or abandon any
> other change request across the entire system.

Consequence claimed: the concurrency model is broken, and a user submitting
a heavy configuration is an unintentional denial of service.

**What to check.** Whether the lock genuinely spans the nix calls; whether
the git worktree model actually requires exclusion (it may - worktrees on
one repo are not freely concurrent); and if so, whether the fix is a
narrower lock, a per-change worktree, or moving the build off the request
path entirely. Note that serialising nix builds may be deliberate: two
concurrent evaluations of the same fleet contend for the same store and
cache anyway.

### 2. Dual-write split-brain between git and Postgres

`ChangeService.Merge` merges in git first, then transitions and persists:

```go
if err := s.repo.MergeNoFF(ctx, cr.Branch, ...); err != nil { ... }
if err := cr.Transition(change.Merged, s.clock.Now()); err != nil { ... }
if err := s.store.Put(ctx, cr); err != nil { ... }
```

If `store.Put` fails after `MergeNoFF` succeeds, git holds the merge while
the database still reports `Ready`. Likewise, if the remote push fails at
the end, local git and Postgres both say `Merged` while the remote does
not have it. There is no reconciliation loop or saga.

**What to check.** This is a design question, not a patch: which system is
the source of truth, and what does recovery look like. Git is already the
authority for configuration (the whole architecture says so), which argues
for reconciling Postgres from git on startup rather than trying to make the
two writes atomic.

### 3. Production image ships more than the server

The Dockerfile builds `sextant`, `sxctl` and `fleetsim` and copies all
three into the runtime image, so a simulation tool and a CLI ride along in
the production control-plane container. Suggested: separate target stages
so the production container carries only the daemon.

**What to check.** Cheap and unambiguous if true. Worth confirming whether
`sxctl` is deliberately present for in-container administration before
removing it.

### 4. `InsecureSkipVerify` is reachable from configuration

`internal/adapters/ldap/ldap.go` exposes `InsecureSkipVerify bool`. The
comment says labs only, but nothing prevents an operator from setting it in
production, where the control plane would then accept forged TLS
certificates from the directory.

**What to check.** Whether it can be set from fleet configuration or only
from deploy-time environment, and whether a refuse-in-production guard (or
removing the knob) is the better answer. Related: the device-side SSSD
module makes the same trade-off deliberately and documents why
(`bb-open/modules/integrations.nix`, certificate policy follows transport)
- the console's own bind is a different decision and deserves its own.

### 5. Unbounded nix evaluation as a DoS vector

A sufficiently recursive or infinite nix expression makes `nix eval` consume
memory and CPU until the context times out; combined with finding 1, that
blocks the whole control plane rather than one request.

**What to check.** What limits the gate runner actually has today (timeout,
memory cgroup, `--max-jobs`), and whether the eval should run under a
resource-capped sandbox. Design 0003 (gate=eval sandboxing) is the existing
home for this.

## What the reviewer credits

Recorded because it says which invariants an outsider could verify by
reading, and those are worth not breaking:

- **The domain layer is pure.** `internal/domain` imports no I/O, no
  adapters and no application code, which is why its tests are instant.
- **Batched writes.** The Postgres adapter uses `pgx.Batch` (e.g.
  `DiscoveredStore.Report`) to put DELETE and INSERT in one round-trip
  instead of an N+1 pattern.
- **HTTP hardening.** `internal/http/mw/mw.go` enforces CSP, HSTS and
  X-Frame-Options; CSRF uses `subtle.ConstantTimeCompare`; handlers wrap
  bodies in `http.MaxBytesReader`. No TODO or FIXME comments near a
  security boundary.
- **Four-eyes is in the domain, not the UI.** `ChangeService` rejects a
  merge where author and approver are the same identity.
