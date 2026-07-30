# ADR 0017 - A policy is a layer over settings, and over more than settings

Status: Accepted (2026-07-30)

## Context

Sextant has settings and it has policies, and an operator opening the console
cannot tell which one to reach for. Both let you change a value. Both apply to
a scope. The overlap is real and it is visible, and left alone it produces the
worst outcome: the same control configured from two places, with nobody sure
which one won.

The question that forced the decision (Bram, 2026-07-30): are policies things
outside the settings, or something else?

## Decision

A **setting** is a key from `catalog.json` - a `dawo.*` option the overlay
publishes - with a value, resolved along the scope ladder (org, group, device).
It answers *what is this device configured to be*.

A **policy** is not a second mechanism for that. It is a wrapper around
settings that adds three things a bare setting cannot express:

- **Intent.** A name and a reason, which an auditor can read and a successor
  can understand. A setting records a value; a policy records why.
- **Enforcement.** Locks, so a lower scope cannot weaken it. A setting is
  overridable by design - that is what the ladder is for.
- **Drift.** A statement that gets re-checked, rather than a value that gets
  written once and forgotten.

So: **settings are the mechanism, policies are the instrument of governance.**

And a policy is not limited to wrapping settings. It is a layer ON TOP of
them, which means it can also assert things that have no setting behind them
at all - conditions about a device's observed STATE. Disk pressure. Battery
health. How long since it last checked in. Free space before an update may
start. These are requirements an organisation genuinely has, and none of them
is a value you write to a device.

That gives a policy two kinds of clause, and the difference is not cosmetic:

- **Configuration clauses** wrap settings. They can be ENFORCED: the fleet
  converges on them, drift is detected and corrected, a lock stops a lower
  scope weakening them.
- **Condition clauses** assert something about observed state. They cannot be
  enforced, because there is nothing to write - a disk does not become emptier
  because a policy says so. They can only be CHECKED, and a failure is a
  finding: report it, raise it, and let a human or a separate action respond.

Confusing the two would be the expensive mistake. A condition presented as if
it were enforceable promises something the system cannot deliver, and an
operator who sees "enforced" next to "disk under 85%" will believe the fleet
is keeping it that way. It is not; it is watching.

The practical rule that follows, and the one to apply when deciding where a
new control belongs: **a setting becomes a policy by default; a policy never
becomes a setting.**

The direction matters more than it looks. If a bare setting is the default,
every new control starts OUTSIDE governance and somebody has to remember to
promote it - and nobody does, because the moment to remember is the moment
you are busy adding the control. Defaults decide outcomes. Making policy the
default means governance is the norm and a plain setting is a deliberate,
justified exception: this one is a local operational choice with nothing to
demonstrate, and here is why.

The asymmetry is the other half. A policy must never quietly degrade into a
setting, because that loses enforcement and the audit trail without anyone
deciding to lose them - and it loses them silently, which is the worst way.
Demoting a policy is a decision somebody makes on purpose and records, not
something that happens by editing a value.

An auditor does not read settings. They read policies. "BIO control X is
enforced on every workplace" is a policy sentence. "netbird.enable = true on
group bb-laptops" is a setting sentence. Both may describe the same bytes on
disk; only one of them is evidence.

## Consequences

- The console must say this, not merely embody it. A settings page that
  silently offers a key with a compliance story is inviting the wrong tool.
- A setting governed by a policy should not present itself as freely editable
  in the general editor. The lock already exists; the affordance contradicts
  it. This is the same principle as the existing one-key-one-place rule that
  keeps integration keys on their card.
- Some controls should be reachable ONLY through a policy - posture, USB
  device control, offline login validity. They are exactly the ones with an
  audit story, and exactly the ones where a quiet local override is a finding.
- New `dawo.*` options land in a policy unless someone argues otherwise, and
  the argument belongs in the option's own documentation. "It is only a
  setting" should have to be said out loud.
- The policy model needs to carry both clause kinds, and the console must show
  which is which - enforced versus checked. That distinction is what keeps the
  word "policy" honest once conditions live in it.
- `Policy.Controls` (the BIO/ISO annotations) is what turns a policy list into
  an auditor-facing view, and it is why the distinction is worth the work
  rather than being a naming exercise. It is live: editable on the policy
  form, shown as tags on the policies page, and carried into the CSV export.

## Status of the consequences (2026-07-30)

Done:

- The settings page says that a control belongs in a policy by default, and
  links there.
- Every setting row names the policies that already carry it at or above the
  scope being edited, and marks the ones a policy locks - the case where an
  edit here would be accepted and then do nothing.
- Conditions are evaluated against what devices report, and a failure becomes
  a finding on the compliance board. The metric vocabulary is a closed list,
  so a condition can only require something the fleet actually measures, and a
  metric a device did not report is unknown rather than failed.

Deliberately not done yet - **policy-only controls**. Making a control
unreachable outside a policy is the strongest form of this ADR and the right
end state for USB device control, posture and offline login validity. It is
held back because it removes a path operators are currently using: the USB
allowlist is set through the settings editor today, including in the
acceptance run this was written alongside. Changing how a control is reached
on the evening it is being tested would invalidate the test rather than the
control. It should land immediately after, as its own change, with the
migration for anyone who set those keys inline.

## Alternatives considered

**Fold policies into settings** - one surface, one mental model. Rejected: it
loses enforcement and drift, and there is then no artefact to hand an auditor.
The organisations this is built for have to demonstrate compliance, not merely
achieve it.

**Fold settings into policies** - everything is a policy. Rejected: most
configuration is an ordinary operational choice with no compliance story, and
wrapping every keyboard layout in a governance object is ceremony that teaches
people to ignore the ceremony.

**Leave the overlap and document it** - what we had. Rejected because it is
not a documentation problem: two paths to the same control is a correctness
question the moment they disagree.

## Related

- The one-key-one-place rule (integration keys live on their card, not in the
  general editor) is the same instinct applied narrowly; this generalises it.
- Design 0001 makes posture image-time, which is precisely the kind of fact a
  policy should state rather than a setting silently imply.
