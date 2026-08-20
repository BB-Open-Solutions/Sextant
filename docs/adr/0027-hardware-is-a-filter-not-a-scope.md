# 27. Hardware is a filter, not a fourth scope

Date: 2026-08-20

## Status

Accepted.

## Context

A fleet wants settings that follow the machine rather than the organisation.
The case that prompted this: a Lenovo in the `infra` group needs a driver for
its fingerprint reader, and a Dell in the same group must not get it. Group
membership cannot express that - both machines are in `infra` - and putting the
setting on each device by hand does not survive the fleet growing.

The obvious answer is a fourth scope. Settings resolve org → group → device;
add `hardware:<profile>` between group and device, and the driver lands on
every Lenovo and nowhere else.

That answer is wrong, because the product already does this.

A policy carries settings and a reason. An assignment binds a policy to a
scope and may name a **filter**, and the filter vocabulary has included
`hardware` since filters existed. The case above is:

```json
"filters": { "is-lenovo": { "rules": [
  { "attr": "hardware", "op": "eq", "value": "lenovo-t495s" } ] } },
"assignments": [
  { "policy": "fingerprint", "target": "group:infra", "filter": "is-lenovo" } ]
```

Measured 2026-08-20 against the resolver: the Lenovo resolves
`fprint.enable = true`, the Dell resolves nothing for that key. No change to
the domain was needed to get that result.

## Decision

We do not add a hardware scope. Hardware stays a **filter attribute**, and
per-model configuration is a policy assigned with a hardware filter.

What we add instead is the path to it. The mechanism existed and was
invisible: nothing in the console suggests that per-model configuration is
possible, so an operator who wants it either edits every device by hand or
asks for a feature that is already there.

## Why not the scope

**It would be a second way to say one thing.** Two mechanisms for per-model
settings means every operator has to know which one a fleet used before they
can predict what a device does, and every reviewer has to check both.

**A fourth precedence rule.** ADR 0026 removed `priority` for exactly this
reason: specificity and inline-over-policy already answer the question, and a
third rule made the answer unpredictable. A hardware scope adds a fourth, and
a genuinely awkward one - hardware is not *more specific* than a group, it is
a different axis. Slotting a second axis into a single specificity ladder
means picking an order and defending it forever.

**It costs the parity harness.** `nix/resolve.nix` is the twin of the Go
resolver, and `parity_test.go` proves them equal on shared fixtures. Every
scope added is added twice, and a difference between the two means the console
shows one thing and Nix builds another. That risk is worth taking for
something the product cannot otherwise do. It is not worth taking twice for
something it already does.

**A policy records the reason; a scope does not.** `fprint.enable = true` on a
`hardware:lenovo-t495s` scope says a Lenovo has this setting. A policy named
"Fingerprint reader (Lenovo T495s)" says why it exists, is reusable across
fleets, and shows up in the audit trail as a decision rather than a value.

## Consequences

- Per-model configuration is available today, in every fleet, with no
  migration and no version bump of the fleet document.
- The console gains a hardware view and a one-step path from a model to a
  policy filtered on it. The filter is created if it does not exist; the
  operator writes the policy, not the plumbing.
- The resolution ladder stays three deep, and the parity harness keeps
  proving one thing rather than two.
- A model with no policy behind it is not special. It is a name devices carry
  and the imaging catalog describes, which is what it already was.

## What would change this

A fleet that needs per-model settings *enforced* against a group - the
governance direction, where the more general scope wins - cannot express that
with a filter, because a policy contributes at the scope it is assigned to. If
that case turns up in practice, the answer is likely to let an assignment
enforce, not to add a scope.

The other signal would be volume: if a fleet ends up with a policy per model
and nothing else in them, the filter is being used as a scope and the model is
wrong. The hardware view makes that countable, which it was not before.
