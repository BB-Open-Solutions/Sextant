# ADR 0017 - A policy composes settings; it is not a second way to set them

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
- `Policy.Controls` (the BIO/ISO annotations) is currently inert. It is the
  hook that makes this pay off: it is what turns a policy list into an
  auditor-facing view, and it is why the distinction is worth the work rather
  than being a naming exercise.

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
