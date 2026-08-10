# 0023 - Devices pull, and the conditions under which that would change

## Status

Accepted 2026-08-10 (Bram). Records a decision the product has held since it
began and never wrote down.

Four items in `docs/competitive-intake.md` (C8, E5, F1, F2) carry the
conflict label "ADR: pull-only". **There was no such ADR.** The rule lived as
prose in three places - `docs/architecture.md`, `docs/capabilities.md` ("No
remote code execution channel exists by design") and `docs/threat-model.md` -
so an item that wanted to argue with it had nowhere to argue.

That is the reason this exists. The strongest guarantee the product makes was
the one with no decision record, which meant it could neither be defended with
a reason nor revisited with a procedure. Both are worse than the rule being
wrong.

## Context

Devices fetch their configuration and converge on it. The console writes to
git; nothing reaches a machine because the console decided to send it. A
remote action is a declarative intent recorded as a gated, audited commit,
which the device picks up on its own schedule.

The reasons, in the order they actually matter:

1. **There is no command channel to abuse.** Not by an attacker who reaches
   the console, not by an operator having a bad day, and not by us. Most of
   what a fleet manager can be misused for needs a channel that does not
   exist here. This is the security argument and it is the strongest one.
2. **A laptop is not reachable and that is normal.** The fleet is mobile:
   shut, asleep, on somebody's home wifi, on holiday for three weeks. A push
   model spends its life failing to reach devices and then has to encode what
   a failure means. Pull makes absence ordinary rather than exceptional -
   which is why `observed.AbsentWindow` exists and why an absent device
   leaves the promotion denominator instead of holding a wave.
3. **The configuration is auditable because it is data first.** Everything a
   device does had to be a commit before it was an action, so the git history
   is a complete record by construction rather than by discipline.

The cost is stated plainly in `capabilities.md` and is not hidden: a
declarative pull model cannot perform a classic MDM wipe, because an offline
device executes nothing. What we offer instead is a crypto-wipe that lands at
the next boot, and we say so rather than implying an immediacy we do not
have.

## Decision

**Devices pull. The console has no channel that executes on a device.**

This is a property of the product, not a default. An item that requires the
console to initiate execution on a device is refused unless the conditions
below are met, and refusing it is not a technical judgement - it is this
decision applied.

## What would change it, and this is the part that was missing

The reasoning above is conditional on the shape of the fleet, and that shape
is not permanent. Two changes would reopen it, both named by Bram on
2026-08-10:

1. **A server estate.** Servers are the opposite of laptops on every point
   that made pull right: always on, in a known location, reachable, and
   operated by people who expect an action to happen now rather than at the
   next converge. Reason 2 above evaporates entirely for that fleet shape,
   and reason 1 becomes a trade rather than a free win.
2. **More than one operating system.** Pull-convergence assumes something on
   the device that converges. A target without a NixOS-style declarative
   agent has nothing to converge with, so "pull" stops describing a design
   and starts describing an absence.

Neither is hypothetical enough to ignore and neither is close enough to
build for. What matters is that they are written here, so the argument can be
**re-run against the conditions** rather than re-litigated from first
principles by whoever raises it next.

**A mixed answer is allowed.** Nothing in this decision requires one
transport for every device class. A push path for servers alongside pull for
workstations is a legitimate outcome of reopening this; what is not
legitimate is acquiring one by accident, for one feature, without the
argument.

## Consequences

- The four conflicted intake items are answered rather than parked:
  - **C8** (device-local non-root `upgrade`) is **probably not a violation**
    and is the one worth arguing. It is locally initiated and still pulls;
    nothing is sent. The argument still has to be won rather than assumed,
    and this ADR is where it gets recorded when it is.
  - **E5** (installer as a Tor onion service) and **F1** (transport ladder
    with automatic fallback) are inbound reachability. They score value 2,
    so the argument is not worth having yet; if the conditions above arrive,
    they return with it.
  - **F2** (P2P SSH break-glass) is the real conflict. It is a channel, armed
    or not. It stays at 2.0 and stays conditional on this ADR being reopened -
    not on a threat-model edit, which is where it was pointed before and which
    would have moved the decision into a document that describes risks rather
    than making choices.
- `capabilities.md`, `architecture.md` and `threat-model.md` keep their
  wording; they now have something to cite instead of each asserting it
  independently.

## Verification

The absence is the property, so the check is that no channel appears. Today,
measured: a device runs `comin` against the forge and `sextant-agent` /
`sextant-actd` against the console, and none of them accepts an instruction to
execute - `actd` claims intents that are already commits. `docs/runbooks/
disaster-recovery.md` records the same split from the other direction: losing
the console does not change what any machine runs.

A future item that adds an executing path must amend this ADR in the same
commit. If it does not, the reviewer's question is not "is this feature good"
but "which conditions above were met".
