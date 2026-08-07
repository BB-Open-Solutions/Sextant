# Processing register (GDPR art. 30) - what Sextant holds about people

Status: **draft, measured against production on 2026-08-07.** Every row below
was read out of the running deployment or located in the code, not inferred
from the design. Where something is not implemented, it says so.

## Who is who

This is the point most product-side registers get wrong, so it comes first.

- **The municipality is the controller.** They decide that a fleet is managed,
  which staff get devices, and what is done with the results. Zaanstad's own
  art. 30 register has to carry these processings; this document exists so
  they can fill it in without reverse-engineering the product.
- **The operator of the console is a processor** on their behalf. For
  bb-open's own fleet that is bb-open, controller and processor in one, which
  hides the distinction rather than removing it.
- **Sextant itself is software, not a party.** It processes nothing on its
  own account.

A data processing agreement (verwerkersovereenkomst) between the municipality
and whoever runs the console is therefore required. **None exists today.**

## What is processed

| # | Data | Where | Subjects | Purpose | Retention as built |
|---|---|---|---|---|---|
| P1 | Assigned user per device (`AssignedUser`, a name or account) | `fleet.json`, in git | staff | know whose device this is, for support and for a lost/stolen decision | **indefinite, and in git history forever** |
| P2 | Console operator identity: subject, e-mail, display name, group memberships | `seen_users` in Postgres | IT staff | render "who approved this" without a directory round-trip | **indefinite, never purged** |
| P3 | Operator preferences | `user_prefs` | IT staff | remember page settings | indefinite |
| P4 | Audit trail: who changed what, when | git commit history | IT staff | accountability, and required by BIO | indefinite by design |
| P5 | Elevation requests: user, device, action, free-text reason, decision and decider | `elevation_requests` | staff | let a user ask an operator for one privileged action | **indefinite, never purged** |
| P6 | Device check-ins: revision, phase, error text, CPU/memory, last seen | `device_status` | indirectly staff, through the device they use | know whether a device is converged and healthy | **indefinite, never purged** |
| P7 | Device facts: kernel, hostname, system path (measured 2026-08-07: those three) | `device_facts` | indirectly staff | inventory | overwritten per check-in, never deleted |
| P8 | **Diagnostics bundles: journal fragments** | `device_diagnostics`, sealed | staff, potentially in depth | support, when a device misbehaves | **14 days**, enforced on read (`app/diagnostics.go:21`) |
| P9 | Notifications addressed to operators | `notifications` | IT staff | tell somebody a change needs review | **indefinite, never purged** (95 rows on 2026-08-07) |
| P10 | LUKS recovery keys | `device_secrets`, sealed | not personal data, but it unlocks a person's disk | get a user back into their encrypted device | indefinite by design |

Not processed, and worth stating because people assume otherwise: no
keystrokes, no screen contents, no browsing history, no location, no
application usage per user, and no interactive remote control
(`docs/capabilities.md:45` records that as a deliberate refusal).

## The special one

**P8 is the sensitive row.** A diagnostics bundle carries journal fragments
from a staff member's machine. Journals are not selective: they can contain
file paths, usernames, hostnames of things the person connected to, and error
messages carrying whatever the application put in them. `docs/design/0010`
recognised this and answered it with three controls that are actually built:
the bundle is sealed at rest, it is requested per device rather than
collected continuously, and it expires after 14 days.

What is NOT built: the user on the device is not told a bundle was taken.
That is a transparency question (art. 13/14) rather than a security one, and
it is the sharpest open point in this register.

## Retention: the honest picture

One retention window is implemented (P8, 14 days). **Everything else grows
without bound.** That is defensible for the audit trail, which is supposed to
be permanent, and it is not defensible for P2, P5, P6 and P9 - an elevation
request from two years ago naming a person and what they wanted to do has no
purpose left.

This is a gap in the product, not only in the paperwork. Art. 5(1)(e) wants
storage limitation, and "we never delete" is a decision nobody made.

## Rights of data subjects

| Right | Status |
|---|---|
| Access (art. 15) | possible by hand (SQL plus a git log); no product surface |
| Rectification (art. 16) | `AssignedUser` is editable; the git history is not, by design |
| Erasure (art. 17) | **not implemented.** Removing a device leaves its check-ins, notifications and elevation requests |
| Objection, restriction | not applicable in a normal employment context, but not implemented either |

The interaction between erasure and the audit trail is a real tension and
needs a written position rather than a shrug: an append-only git history is
required by BIO and is exactly what art. 17 asks to be able to remove from.
The usual resolution is that accountability data has its own legal basis and
retention, and that the register says so explicitly. **It does not yet.**

## What has to happen before Zaanstad

1. **A DPIA.** This register is its input, not its substitute. P8 (journal
   fragments from staff machines) is what makes a DPIA more than a formality.
2. **A processing agreement** between the municipality and the console
   operator.
3. **Retention windows for P2, P5, P6, P9**, implemented rather than
   documented, plus a written position on the audit trail.
4. **Transparency on P8**: either the device tells its user a bundle was
   taken, or the municipality's own privacy statement covers it - a decision
   somebody has to make, not an omission somebody can inherit.
5. **An erasure path** for a departed employee, even a documented manual one.

## What this document is not

It is not legal advice and it is not signed off by anybody. It is the factual
inventory a DPO needs in order to do their work, written by the people who
know where the data actually is. The measurement dates are in it so that a
reader can tell how stale it has become.
