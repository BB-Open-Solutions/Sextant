# Roadmap after 1.0

What 1.0 contains is decided elsewhere: `1.0-fit-gap.md` holds the scope and
the gate. This document starts where that one stops.

An earlier roadmap was deliberately deleted, because it restated the 1.0 scope
that the fit-gap already owned and the two drifted apart. This one does not
touch 1.0 at all. If something here turns out to belong in 1.0, it moves to the
fit-gap and leaves - it is never described in both. It says what comes next,
in what order, and - more usefully - **what forces each item**, because a
roadmap that is only a wish list gets reordered by whoever shouts loudest.

Dates are deliberately sparse. Each release below names its **trigger**: the
thing that makes it urgent. Where a trigger has a date, the release inherits
it. Where it does not, the release ships when the work is done rather than on a
calendar we invented.

## How we work from 1.0 onward

Until now, changes went straight to `main` with the pre-push hook as the gate.
That was right for a project with one author and a fleet of two, and it stops
being right the moment somebody else can contribute.

From 1.0.0:

1. **An issue first.** It states the problem and how you would know it is
   fixed. Not the solution.
2. **A branch and a commit** that explains *why*, in the style the repository
   already uses.
3. **A merge request.** CI green, review, then merge.
4. **Releases are tags on `main`**, cut after the merge - never before. That
   rule came from Rutger and applies here for the same reason: a tag ahead of
   review is an unreviewed release.

The one exception is a production incident, and it is written down so it stays
an exception: fix forward, then open the issue and the merge request
retroactively, with the incident named in the commit.

## Where the code lives, from go-live onward

**Trigger: go-live.** Decided 6 August 2026.

`forgejo.bb-open.com` stays the repository we actually work in: commits,
CI and Flux all read from it, and that does not change. What changes is the
public side. **Codeberg becomes a second mirror alongside
`code.overheid.nl`**, both reflecting what happens on forgejo, and the
GitHub repository goes offline.

This is deliberately not a migration. Nothing about the Go module path, the
`.forgejo/workflows/` pipeline or the self-hosted nix runner moves, because
the place the work happens is not moving. Mirrors are push targets.

Two things still to settle, and neither blocks the mirror itself:

- **Whether Codeberg also runs CI.** A mirror does not need it, but a public
  repository that shows no build status invites the question. If it does,
  that is Woodpecker and a second runner with nix - the release workflow
  assumes one.
- **What the public mirror is called.** The Codeberg repository is
  `DAWO/SextantFleet` while this one is `DAWO-Sextant`. Harmless for a
  mirror; worth a decision so the two names do not read as two products.

Also worth knowing before somebody wires it up: `upstreamRepo` in the
HelmRelease points at `code.overheid.nl/MinBZK/DAWO-NixOS.git`, so the core
repository has its own mirror question, separate from this one.

## 1.1 - what Zaanstad hits first

**Trigger: the first machines that are not pilot laptops.** Everything here is
already known to be wrong; it simply has not mattered on a fleet of two.

- **Multiple drives, and stable disk addressing (#49).** Two disko profiles
  exist and both hardcode `/dev/nvme0n1`. A second drive is not partitioned,
  not encrypted and not wiped - so a crypto-wipe destroys the keys of one disk
  and leaves the other readable, which nobody expects from the word "wipe". And
  enumeration order is not a promise: two NVMe drives and "the first one" can
  move. `/dev/disk/by-id` is the fix. Core work, so fork and pull request.
- **App profiles in the console (#54).** The additive model already works;
  what is missing is the named, reusable profile. Requires agreeing the
  boundary with the DAWO core first - what stays in the image and what Sextant
  composes.
- **A garbage-collection policy for devices.** A one-day-old laptop already
  carried 101 dead store paths. Not a problem this year; certainly one in a
  fleet nobody prunes.

## 1.2 - governance that a municipality will ask for

**Trigger: the first customer with more than one administrator.**

- **Capability RBAC on directory groups (#53).** Today the model is
  Viewer/Editor/Owner scoped by group, which is the right shape and too coarse.
  Permissions on a small set of named capabilities - start a rollout, approve a
  wave, wipe a device, reveal a secret, change endpoint controls, change access
  itself - each bound to a directory group at a scope.
- **Four-eyes narrowed to where it earns its place.** The rule, not a list:
  *a change the gate and the test ring can prove is fine needs no second
  person; a change that removes the gate, removes the ring, or removes the
  ability to recover, does.* The mechanism already exists as `riskClass` in the
  catalog and is used on exactly two options today.

- **A reseller portal.** Cell provisioning stays manual for 1.0 - a
  template directory and a runbook, `cp` and `sed`, decided 2026-07-28
  and reconfirmed 2026-08-05. What replaces it is not a scaffolder for
  us but a portal where customers create and manage their own
  environments. That is a different product surface with its own
  tenancy boundary, so it waits until the cell shape has been proven by
  provisioning and retiring real ones by hand.

## 1.3 - reach

**Trigger: an operator who is not at their desk when the notification arrives.**

- **The console on a phone (#48).** Server-rendered HTML with no JavaScript
  requirement is the best possible starting point and the viewport work is
  already partly done. The job is the six tables and the sidebar. Decide
  explicitly which actions belong on a phone - approving a wave and locking a
  lost device, yes; editing the whole settings tree, probably not.

## Unscheduled, and honest about why

These matter and none of them has a trigger yet. They move up the moment one
appears.

- **Tenant isolation for the gate.** A cold edit blocks for one evaluation.
  On a single console that is honest - the operator asked for something new.
  With several tenants on one gate slot it is not, because one organisation's
  cold edit becomes everybody's wait. This belongs with the multi-tenancy
  design, not with the write path. Measured and reasoned in
  `architecture/scale.md`.
- **Parallel evaluation workers, and profiling the 3 GiB.** Each worker needs
  roughly 3 GiB, so this waits on moving the gate off-cluster. And nobody has
  looked at where that 3 GiB actually goes - it is high for a NixOS toplevel.
- **Sovereign flake mirrors and our own cache as the fleet substituter**
  (ADR 0016). The fork exists as phase one.
- **OpenBao as the cell-secret backend**, plus HA, auto-unseal and TLS. Note
  the ordering trap recorded in the OpenBao task: an admin recovery path must
  not run through a secret that only the thing being recovered can decrypt.
- **SCIM inbound, and LDAP direct-bind as a console auth source.**
- **comin records a failed switch as successful (#55).** The control plane no
  longer believes it, so this is about the story an engineer reads while
  standing in front of a broken machine. Establish first whether it is upstream
  behaviour.

## What we are deliberately not doing

Kept here so the question stops coming back.

- **Forge drivers.** Provisioning is manual by decision. A driver interface
  returns only if provisioning volume ever demands it.
- **A remote command channel.** Devices pull; the console never pushes. Every
  remote capability is an *intent* the device chooses to act on. This is not a
  limitation to be engineered away - it is the reason there is no channel for
  an attacker to abuse either.
- **A crippled edition.** Everything is in this repository under the EUPL.
  What BB Open sells is work, not permission.

## Keeping this honest

Two habits, both learned the expensive way:

**Measure before building.** The asynchronous validation queue was in this
roadmap until it was timed: an ordinary edit holds the write lock for fifteen
milliseconds, so the queue would have optimised a path that is already fast and
changed what "saved" means to an operator. It was removed on the strength of a
measurement rather than an opinion.

**A finding without a trigger is a note, not a plan.** Everything above either
names what forces it or sits under "unscheduled" and says so.
