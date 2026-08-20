# Configure a hardware model

Some settings follow the machine rather than the organisation. A Lenovo needs
the driver for its fingerprint reader; a Dell standing next to it in the same
group does not. Group membership cannot say that - both are in `infra` - and
setting it on each device by hand stops working the day the fleet grows.

The **Hardware** page is where that is said once.

## What the page shows

One row per model the fleet runs. That is the union of two lists, because
either one alone would mislead:

- the models the overlay's `hardware-profiles.json` describes, whether or not
  any device carries one yet;
- the models devices actually report, whether or not the overlay has heard of
  them.

A model in the second list and not the first is flagged **not in the catalog**.
That is worth seeing: nothing can image one, because imaging needs the model's
disk layout and steps. The fix is in the overlay, not in the console.

## Configuring one

**Configure this model** opens a window with the model's settings, the keys it
locks, and who it applies to - the whole fleet, or only the devices of that
model inside one group.

Saving writes three things in one commit: a policy holding the settings, a
filter that selects exactly that model, and an assignment binding them. That is
what an operator could always have assembled by hand, and the reason this page
exists is that assembling it by hand took three edits in the right order, so
nobody did.

It is written as a policy on purpose. A policy carries a name and a
description, so `fprint.enable = true` stops being a value on a machine and
becomes "Fingerprint reader (Lenovo T495s)" in the audit trail, reusable and
explainable six months later.

Configuring again is editing: the settings are refreshed and the assignment
moves if you changed who it applies to. It never leaves a second one behind.

## Removing it

**Remove configuration** deletes the policy and the assignment. The filter
survives if another assignment still points at it - it is a named thing
somebody may have reused.

## Why hardware is not a scope

Settings resolve organisation → group → device. Hardware is deliberately not a
fourth level in that ladder: it is a different axis, not a finer one, and a
second mechanism for per-model settings would mean every operator has to know
which one a fleet used before they can predict what a device does. See
[decision record 27](../architecture/adr.md).

The practical consequence: a hardware policy contributes at the scope it is
assigned to. If the organisation enforces a key, the model's setting does not
override it - enforcement runs the other way, from general to specific, and
that is the governance direction on purpose.
