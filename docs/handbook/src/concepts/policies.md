# Policies, and how they differ from settings

A setting is a value. A policy is a value **with a reason attached**, and that
difference is the whole point: an auditor asking "why is USB storage blocked on
these laptops" wants a name and a justification, not a key with `false` next to
it.

Policies are a layer *over* settings, not a wrapper around them. They can do two
things a setting cannot: carry the compliance controls they implement, and state
requirements about a device's **observed state** rather than its configuration.

## What a policy carries

| | |
|---|---|
| **Settings** | The values it applies, exactly like a scope's own settings. |
| **Enforced** | Which of those keys are locked, so a lower scope cannot weaken them. |
| **Description** | Why this exists, in a sentence an auditor can read. |
| **Controls** | The framework references it satisfies, e.g. `BIO 12.3.1`, `ISO 27002 8.9`. Free text; the console and the evidence export carry them through. |
| **Conditions** | Requirements on what a device *reports*, not on what it is configured with. |

## Enforced versus checked - the distinction that matters

This is the one thing worth reading twice.

A **setting** can be enforced. The fleet converges on it, drift is corrected,
and a lock stops a lower scope weakening it. If the value is wrong today, the
system makes it right.

A **condition** can only be checked. There is nothing to write: a disk does not
get emptier because a policy says it should be. A failing condition is a
**finding to report**, never a state to converge on.

Showing "enforced" beside a free-disk-space requirement would promise something
the system cannot deliver, so the console does not. Conditions appear as
findings; settings appear as configuration.

There is a second rule that follows from the same honesty, and it is deliberate:
**a device that never reported a metric is not accused of failing it.** An older
agent, or a probe that did not run, produces silence - and silence is unknown,
not failure. A board that reports "disk below 15%" for machines it cannot
measure teaches operators to ignore the finding, which costs more than the
finding was worth.

## Assigning a policy

A policy on its own does nothing. An **assignment** binds it to a scope:

- **Target** - `org`, `group:<name>` or `device:<tag>`.
- **Filter** - optional; narrows the assignment to devices matching a rule set
  (all rules, or any). Without one it covers every device in the target.
- **Priority** - decides which policy wins when two set the same key.

So the same policy can be assigned broadly and narrowed by filter, rather than
copied per group with small edits. Editing the policy updates everywhere it
applies.

## Where the values end up

Policy settings resolve into a device's effective configuration alongside
ordinary scope settings, along the usual organisation → group → device chain.
The device page shows each value with where it came from, so "set by policy
*Baseline hardening*" reads the same way as "set by group *bb-laptops*".

An enforced key from a policy behaves exactly like an enforced key on a scope:
a lower scope may not weaken it, and the console says so rather than silently
discarding the edit.

## Profiles and drift

A policy created from an overlay-published profile records where it came from,
as `name@hash`. The console then keeps the two in view and distinguishes three
different ways they can disagree:

- **Reapply** - the overlay's profile moved on and this policy did not.
- **Edited** - the policy has been changed by hand since it was applied. The
  stamp alone cannot see this; it is found by comparing the actual settings.
- **Conflict** - a hand-made policy has taken the id a profile wants.

A profile that was never instantiated simply offers to be applied, and one that
matches reads as current.

All of it is provenance. Resolution ignores the profile entirely; nothing
changes on a device because a profile drifted. It exists so an operator can see
that the recommendation they started from has moved, and decide - which is a
different thing from being moved for them.

## When to use a policy instead of a plain setting

Use a plain setting when the value is simply what you want. Use a policy when
somebody will eventually ask *why*, or when you need to say the same thing in
several places without repeating yourself:

- A rule you must justify to an auditor, with the control reference attached.
- A rule that applies to a set of devices defined by a property rather than by
  group membership - that is what filters are for.
- A requirement about observed state, which a setting cannot express at all.

For "turn this app on for this group", a setting is the right tool and a policy
is ceremony.
