# 28. Two control planes, one reporting plane

Date: 2026-08-23

## Status

Accepted.

## Context

Almost nobody runs one operating system. A fleet that has NixOS in it also has
macOS laptops, Windows machines and phones, and the tool people name in the
room for those is Fleet (FleetDM). The question that keeps coming back is
whether Sextant should become part of it: do everything in Fleet, and keep a
Sextant module beside it for what Fleet does not do, such as imaging.

It is worth taking seriously, because the alternative - asking an organisation
to run two consoles - is a real cost and nobody enjoys paying it.

The reason it does not work is that the two products disagree about where the
truth lives, and that disagreement is not a detail to be smoothed over.

Fleet observes. Agents report, osquery answers questions, and configuration is
pushed out as MDM profiles. The database Fleet keeps is authoritative: it is
where an operator states intent and where the fleet's state is read back.

Sextant declares. The authoritative statement is a git repository the customer
owns, devices pull it themselves with comin, and the console never opens a
connection to a device (ADR 0023). The observed plane in Postgres is a report
about reality, never a place where intent is stored.

Put a setting under both and neither is wrong on its own terms, but the fleet
is. Fleet pushes a profile; the overlay says something else; the device pulls
and reverts. Fleet then reports drift that never converges, and the operator
sees a device that is disobedient rather than a system that is double-owned.
That failure is quiet, it looks like a device problem, and the logs that would
explain it live in two products.

## Decision

Sextant does not merge into Fleet. The two run beside each other as **two
control planes with one reporting plane**.

The property that has to hold is narrower than it first sounds:

> Single source of truth is per device, not per fleet.

Every device has exactly one system that decides its configuration. Fleet is
that system for the devices Fleet manages. Sextant is that system for NixOS
devices. What is forbidden is two systems holding authority over the same
device, or over the same setting.

From that, three rules:

1. **Sextant accepts no configuration authority from outside.** Not from
   Fleet, not from an MDM, not from an API that writes settings a human did
   not put in the overlay. A change enters through the overlay and the gate,
   or it does not enter.
2. **Sextant pushes nothing at a device**, which is already true and stays
   true. Whatever integration exists cannot become a command channel by
   accident.
3. **Evidence flows one way: out.** Sextant exports what it observes and
   judges - inventory, compliance verdicts, the revision a device runs - so an
   auditor and an operator have one screen. Nothing flows back in.

So the module in the original question exists, and it is an exporter rather
than an embedding. Sextant stays a control plane and becomes, additionally, a
source of facts for Fleet's reporting.

## Consequences

**What this rules out**, deliberately, so nobody builds it later by accident:

- Importing Fleet policies as Sextant settings.
- Applying an MDM profile to a NixOS device.
- Any Fleet-initiated write into the overlay.

Each of those recreates the double ownership this decision exists to prevent.

**What it makes possible:**

- One place to look for compliance across a mixed fleet, without either
  product pretending to manage machines it does not.
- An honest answer to "how does this sit next to Fleet", which is a question
  every evaluation asks and issue #29 is already open about.
- Each product keeps doing what it is good at. Fleet's query surface over
  osquery, macOS, Windows and mobile is a genuine capability we answer
  differently and should not try to replace. Imaging, the gate, rings and
  escrow are ours.

**What is not settled and has to be measured before anything is built:**

Whether Fleet's data model accepts a host it does not manage - a device whose
configuration authority is elsewhere - and how it renders one. If it cannot,
this is a dashboard integration rather than a host-level one, and the exporter
targets a different surface. That answer decides the shape of the work, so it
comes first.

The export format is likewise open. The obvious candidates are Fleet's own
host and policy APIs, or a neutral envelope such as OCSF (issue #17), which
would serve more than one consumer. Choosing between them is a separate
decision and not made here.
