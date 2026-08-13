# 26. A policy conflict is shown, not broken by a number

Date: 2026-08-13

## Status

Accepted.

## Context

Two policies can contribute the same setting key to the same device at the
same scope specificity. Until now an assignment carried a `priority` integer,
and the higher number won.

Three things are wrong with that.

**It answers a question the product answers twice already.** Settings resolve
by specificity (org → group → device) and, at equal specificity, an inline
value beats a policy contribution. Priority is a third precedence rule for the
same question, and the operator has to know which of the three applies before
they can predict what a device will do.

**The number carries no reason.** `priority: 10` records that somebody wanted
this one to win, not why, and six months later nobody can tell whether 10 was
deliberate or a default somebody typed. Everything else in this product is
built the other way round: a policy exists to record the reason, and the audit
trail exists so a change can be explained.

**Nobody else ships it.** Intune assigns to groups with an optional filter and
has no priority number. Jamf scopes to smart groups and exclusions. Fleet
gives a host a team and labels. All three either resolve conflicts by a fixed
rule or show the conflict; none asks the operator to rank their policies.

## Decision

`priority` is removed from resolution.

At equal specificity, when two policy contributions disagree about a key, the
first declared wins - a deterministic rule that is written down, and the same
one that already broke ties when priorities were equal. The console does not
present that as the answer: **the conflict itself becomes a finding**, naming
both policies, the key, and the scope where they meet.

The field stays in the fleet document and is ignored, so an existing fleet
keeps loading. An assignment that still carries one is reported, so the
operator learns it no longer does anything instead of discovering it when a
device takes the other value.

## Consequences

- One precedence rule fewer to hold: specificity, then inline over policy,
  then declaration order.
- A conflict is visible rather than resolved silently. Two policies fighting
  over a key is a modelling mistake - the answer is to make one more specific,
  or to take the key out of one - and the console now says so instead of
  letting a number hide it.
- Resolution stays total: a device always gets a value. This is not a gate
  that refuses; a fleet that cannot render is worse than a fleet with a
  documented tie-break.
- `priority` in an existing `fleet.json` is inert. It is not an error, and it
  is not silent either.
- The CSV export drops the priority column, which is a contract change for
  anybody parsing it.

## What would change this

A deployment that genuinely needs two overlapping policies at one scope, and a
way to rank them, would be evidence that specificity is too coarse. The answer
then is a finer scope, not a number: the conflict finding tells us how often
that case actually occurs.
