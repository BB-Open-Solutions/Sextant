# Supply-chain audit, August 2026

Read against `main` and the bb-open overlay on 2026-08-07. Licences were
determined by reading the licence file shipped with each dependency, not by
trusting a manifest field.

## What was measured

- The Go dependencies vendored into this repository (27 licence files).
- The Rust agent's dependencies.
- The unfree and insecure package allowances in the overlay that builds the
  fleet image.
- What the SBOM covers.

## Findings

### S1 - A permitted-insecure package that nothing uses any more

**Measured.** `bb-open/flake.nix:244` carries:

```nix
permittedInsecurePackages = [ "electron-39.8.10" ];
```

The closure of a workplace device (`e2e5`) contains electron **41.9.1 and
42.5.1**. It contains **no 39.8.10 at all**. The permission grants nothing.

**Why a dead exception is worth removing rather than leaving.** Two reasons,
and the second is the one that matters:

1. It reads as a deliberate decision to ship a known-vulnerable component.
   Anybody auditing this file - and this is exactly the file an auditor
   opens - concludes the fleet knowingly runs an insecure electron. It does
   not.
2. **It is a standing permission with no expiry.** If a future nixpkgs bump
   brings electron 39 back into the closure, it is admitted silently, with
   no decision and nobody looking. The exception has outlived the reason it
   was granted for, which is the same shape as a firewall rule nobody
   remembers opening.

**Verified.** Removing the line evaluates cleanly: the workplace class
builds with no insecure-package permission at all.

**One cost worth stating.** Removing it changes the derivation hash of the
system, so every device rebuilds. If the outputs are byte-identical the
devices substitute and it is nearly free, but that has not been measured -
so this should ride along with a change that was going to trigger a rebuild
anyway rather than being dispatched on its own.

**Severity: low.** Nothing is exposed today. It is hygiene, and the kind
that quietly becomes a real exposure later.

### S2 - No licence check runs anywhere

**Measured, and the result is clean.** All 27 vendored Go dependencies are
BSD (11), MIT (9) or Apache-2.0 (7). No GPL, no LGPL, no unknowns. The
agent's three direct dependencies (serde, serde_json, ureq) are the usual
MIT/Apache-2.0 dual licences. All of it is compatible with this project's
EUPL-1.2.

**The finding is that nobody checked.** Today's answer is clean because the
dependencies happen to be permissive, not because anything enforces it. A
future dependency under a copyleft licence would be vendored, built,
released and shipped with no signal at all - and for an EUPL-licensed
product distributed to public bodies, that is a licensing problem before it
is a technical one.

The check is cheap: the licence files are already vendored, so it is a walk
over `vendor/` rather than a network call.

**Severity: low today, and it is the classic case where "we looked once" is
not the same as "we check".**

**CLOSED 2026-08-07.** `licence_test.go` walks `vendor/` and reads each
licence TEXT - not a manifest field, because a manifest says what somebody
typed and the text says what was granted. It runs with `go test` like
everything else, so it fails on the commit that introduces a bad dependency
rather than in a review nobody scheduled.

Three things make it a real check rather than a green tick. An unrecognised
licence FAILS rather than passing, because one this test cannot read is one
nobody has read. A walk that finds nothing fails too, which is the failure
mode the whole file exists to avoid. And a second test proves the detector
can say no: it identifies GPL, LGPL and AGPL correctly and asserts none of
them is in the allowlist. Verified end to end by seeding a fake GPL
dependency and watching the check refuse it.

Copyleft is not forbidden by law here. It is kept out of the allowlist so
that admitting one is a decision somebody takes deliberately, with the
reasoning written next to the name.

### S3 - The SBOM covers the images, not the fleet

**Measured.** `.forgejo/workflows/release.yml` generates an SBOM per
released image with syft and attaches it to the release. That is real and it
works.

What it does not cover is the thing a municipality actually runs: the NixOS
closure on a device. The console image's SBOM says nothing about what is on
a laptop, and the laptop is where the attack surface is.

Nix knows the answer exactly - the closure is a precise dependency graph,
better than most SBOM tooling can produce - so this is a matter of exporting
it rather than discovering it.

**Severity: medium for procurement, low for security.** It links directly to
the CVE gap already recorded in `iso27002-mapping.md`: without a fleet-side
inventory there is nothing to match advisories against.

## Checked and sound

- **The unfree allowances are explicit and reasoned.** Each of the eight
  entries carries why it is there, and three of them say plainly that they
  are not this fleet's hardware and arrive because the image enables all
  firmware rather than the redistributable set - with the real fix named as
  an upstream question rather than papered over.
- **The distinction between permitting and installing is understood.**
  `displaylink` is permitted and ships nowhere by default; the comment
  states that the binary cache we serve is itself a form of redistribution,
  which is the correct and non-obvious reading.
- **The DisplayLink restriction is disclosed rather than hidden.** The
  README says the fleet supports the docks and this repository cannot carry
  them, and names it as the vendor's restriction. For a product claiming no
  crippled edition, saying where the exception is costs less than being
  found out.
- **Dependencies are vendored**, so a build does not depend on a registry
  being up or a version being un-deleted.
- **The agent has three direct dependencies.** That is a deliberate and
  unusually small surface for something that runs as root-adjacent on every
  device.

## Order of work

1. ~~S2~~ **done.** It is the one that turned a lucky answer into a checked
   one.
2. **S1**, riding along with any change that already forces a rebuild.
3. **S3** after 1.0, together with the CVE reporting gap - they are the same
   piece of work seen from two directions.
