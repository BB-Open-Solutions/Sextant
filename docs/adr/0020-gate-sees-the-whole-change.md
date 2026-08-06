# 0020 - The gate evaluates everything a change carries, not just fleet.json

## Status

Accepted 2026-08-06 (Bram). Extends ADR 0012, which chose the remote gate-runner and
defined what it receives. Nothing here changes the fail-closed rule or the
decision to keep nix out of the console image.

## Context

ADR 0012 says the runner "fetches+resets to the tracked branch, drops in the
candidate `fleet.json`, and runs the existing EvalGate against that tree". That
is exactly what it does, and it is the whole problem: `fleet.json` is the only
thing that travels.

A change that touches anything else is validated against a tree that does not
contain it. Measured on 2026-08-06: a DAWO core update - which changes
`flake.lock` and nothing else - passed the gate, was approved, merged, and left
the overlay's `main` unable to evaluate for the workplace class. The gate had
answered a different question truthfully.

The same hole covers the ADR 0014 overlay editor, whose entire purpose is
committing Nix that must evaluate before it lands.

**The obvious fix does not work.** Naming a ref in the request and having the
runner check it out fails on a fact nobody had checked: change branches are
never pushed. `git ls-remote <overlay> 'refs/heads/cr/*'` returns nothing. They
exist only in the console's own clone.

## Options

**A. Push change branches to the overlay remote.** Makes a ref workable, and
carries a second benefit worth naming: an open change currently lives only on
the console's volume, so losing that volume loses every draft. Against it, the
overlay repository is what devices follow, and filling it with drafts changes
what a reader of that repository is looking at. It also needs a remote branch
lifecycle - `cleanup` deletes locally today and would have to delete remotely
too, with the failure modes that come with a network call in a cleanup path.

**B. Send the files the change touches.** The request carries a map of path to
content instead of a single document; the runner writes them over its
`origin/main` tree exactly as it writes `fleet.json` today, and evaluates that.
A generalisation of the existing protocol rather than a new mechanism.

**C. The runner fetches from the console.** The console would serve its own
git. Most plumbing, and it inverts the direction ADR 0012 chose: today the
runner pulls from the forge and depends on nothing the console holds.

## Decision

**Option A**, with the evaluation target taken from B.

The first draft of this ADR chose B and carved durability out as "a separate
question". That was the wrong shape: it is one question - where the truth about
an open change lives - and answering only the gate half means building B now
and A later, at which point B is redundant machinery for a job A already does.

The objection that decided the first draft does not survive checking. Pushing a
`cr/*` branch is not a new capability: `ports.RefUpdater.PushRef` exists and the
console already force-pushes ring branches to this same remote. The overlay
repository already carries machinery branches; a change branch is the same kind
of thing, and devices follow ring pins, so a draft is inert to them.

So:

- **The console pushes a change's branch** when the change is opened or edited,
  and deletes the remote ref in `cleanup` alongside the local one.
- **The runner fetches that ref and merges it onto `origin/main` in its scratch
  worktree**, then evaluates the result. This is B's best property and it is
  worth keeping: a change is going to be merged INTO main, so the state that
  will exist after the merge is what the gate should be asked about. Evaluating
  the branch in isolation answers a question about a tree that may never exist,
  because main can move underneath it.
- **A merge that conflicts is a gate refusal**, not an error. It is exactly the
  case an approver needs to hear about before merging, and today they find out
  at merge time instead.

Drafts stop living on one volume as a side effect rather than as a second
project.

## Consequences

- **`ports.Gate` grows a way to name the ref under validation.** The in-process
  `EvalGate` ignores it - it already evaluates a real working tree - so the
  local and remote gates keep answering the same question.
- **The overlay repository accumulates `cr/*` branches.** Cleanup already
  deletes them locally on merge and abandon; it must now delete them remotely
  too, and that call can fail - a leaked remote branch is an untidiness, so it
  is logged rather than allowed to fail the merge it follows.
- **A core update becomes gateable.** That is the point, and it is also the
  test: a change carrying only `flake.lock` must be evaluated against the core
  it names, not the core it replaces.
- **An open change survives the console.** Losing the volume today loses every
  draft. That stops being true, and it was the argument that decided this ADR
  rather than a bonus noticed afterwards.

## Before this is binding

A test that a change carrying only `flake.lock` reaches the runner and is
evaluated against the new core - the exact case that got through on
2026-08-06. Without it, this ADR would be a claim rather than a repair.
