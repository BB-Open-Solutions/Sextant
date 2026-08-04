# Endpoint controls

Four settings groups decide what a person can do on their own machine without
calling anyone: USB device control, printing, user rights, and the local
administrator account. They are ordinary fleet settings, so they resolve
organisation → group → device and can be locked like any other.

## USB device control

`usbDevices.enable` turns on USBGuard: a device plugged into a running machine
is blocked unless a rule allows it.

`usbDevices.allowlist` takes extra USBGuard rules, one per line, for the things
that must keep working - a specific model of card reader, a signature pad.

**The enable carries a high risk class**, so the console asks for an explicit
extra confirmation before it changes. That is not ceremony: switching it on
across a fleet stops hardware people are holding in their hands, and switching
it off removes a control somebody signed for.

## Printing

`printing.enable` turns on CUPS. `printing.discover` finds printers announced
on the local network over mDNS/IPP, which is what most offices want and what
most home setups need.

`printing.drivers` chooses the driver set:

- **open** - the standard IPP/PostScript path. Covers most office printers made
  this decade, and keeps the closure small.
- **broad** - the wider vendor driver set, for hardware the open path does not
  reach.

## User rights

`userRights.enable` lets ordinary users change desktop settings that would
otherwise need an administrator. Each right is set individually under
`userRights.options.*`, and each takes one of four modes:

| Mode | Meaning |
|---|---|
| **off** | Nobody but an administrator. |
| **self** | The user may, after typing **their own** password. polkit remembers it briefly, so a run of related steps is not a run of dialogs. |
| **session** | Anyone in a real, foreground session on the machine. |
| **group:&lt;name&gt;** | Only members of that directory group, in a real session. |

**Every mode requires a local, active session** - never SSH, never a background
or remote one. That clause is what makes granting these safe, so it is part of
every rule rather than something a mode can opt out of.

The rights on offer are the ones that otherwise generate a support call from
somebody who cannot work: approving a dock when it is plugged in, applying a
firmware update fwupd offers, editing a network connection that applies to the
whole machine.

### What is deliberately not on the list

Four things are absent, and it is not an oversight:

- **Creating, deleting or re-grouping accounts.** On a fleet machine the account
  set comes from the directory. A local user who can add accounts can add one
  that outlives their own and answers to nobody.
- **Autologin and the greeter's user list.** Autologin turns full-disk
  encryption into a locked door with the key taped to it.
- **Changing the hostname.** The fleet identifies devices by name.
- **Firmware downgrades.** Rolling firmware back to an older version is a way to
  reintroduce a fixed vulnerability, so the upgrade path is offered and the
  downgrade path is not.

They are **absent rather than explicitly denied**. An explicit no would
short-circuit polkit for administrators too, which would take away the path that
is supposed to remain.

## Local administrator

`localAdmin.enable` creates an administrator who can sign in when the directory
or the network is unreachable. `localAdmin.username` is the login name - pick
one per organisation rather than inheriting a default - and
`localAdmin.passwordSecret` names a **secret reference** holding the password
hash, so no credential is shared between fleets and none of it passes through
the console.

This is also high risk class, in both directions. Off locks the account on every
device it applies to; on, it creates a way in that does not depend on your
directory being up.

Related: [Manage secrets](./secrets.md) for registering the password reference,
and [Approve a request for privilege](./elevation.md) for the path a user takes
when they hit something none of these rights cover.
