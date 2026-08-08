# A bill of materials, and CVEs, for what the fleet runs

The release pipeline already produces an SBOM per container image. A
municipality does not run our images; it runs a NixOS closure on a laptop.
This produces the SBOM for that, and a vulnerability report from it.

Nix makes this easier than it is anywhere else. A closure is an exact
dependency graph rather than an inventory inferred from a package database,
and nix packages carry upstream version numbers - the same ones NVD indexes,
not a distribution's backport revisions.

## Run it

```
nix shell github:tiiuae/sbomnix -c \
  nix run github:.../DAWO-Sextant#fleet-sbom -- <fleet-flakeref> out
```

`<fleet-flakeref>` is the overlay that defines the fleet (`.` from inside it).
Add `-hosts a,b` to limit it.

The scanners come from the caller rather than from our lock, and that is
deliberate: a vulnerability scanner pinned six months ago stops seeing
everything published since, which is the exact failure the report exists to
prevent.

## What you get

In `out/`, per **distinct closure** rather than per device - a fleet has many
machines and few configurations, and scanning per machine multiplies one
answer while looking thorough:

| file | what it is |
|---|---|
| `manifest.json` | which hosts share which closure, and the finding count |
| `sbom-<id>.cdx.json` | CycloneDX, the one to keep |
| `sbom-<id>.spdx.json` | SPDX, for whoever asks in that format |
| `vulns-<id>.csv` | vulnix, grype and OSV, cross-referenced |

`<id>` is the store hash, so the same configuration produces the same
filenames and two runs can be diffed.

## Reading the report

**Do not treat the count as a score.** Cross-referencing three scanners is
what makes it usable, but a hit is a candidate, not a finding: a CVE against a
library that is present in the closure and never executed is real for an
auditor and not for an attacker. Triage is a person's job.

The three scanners disagree, and that is the point of running all of them.
First run, 2026-08-08: vulnix 405, grype 427, OSV 43, union 589. Any single
scanner would have given a quieter number and a false sense of coverage.

**Check the pin age before you triage anything.** That same run produced 589
findings, of which 148 were Firefox and Thunderbird - both pinned at 152.x by
a nixpkgs snapshot five weeks old. A report like that is not a security
backlog, it is a bump that has not happened. Look at what the top few packages
have in common before opening a single advisory.

The run does **not** fail on findings. That is on purpose. A report that
breaks the build gets switched off within a month, and then there is no
report at all. What an open CVE blocks is a policy question, and it belongs to
whoever set the policy.

## Re-scanning something you already shipped

Keep the CycloneDX file with the release. Months later:

```
vulnxscan --sbom sbom-<id>.cdx.json --out vulns-today.csv
```

That answers "was 1.0.0 exposed to this?" against a feed that did not exist
when 1.0.0 shipped, without rebuilding it or even having the closure. It is
the reason the SBOM is the durable artifact and the CSV is not.

Note the trade: on `--sbom` input vulnxscan skips its vulnix pass, so a fresh
scan against the closure sees slightly more. Scan the closure while you have
it; keep the SBOM for when you do not.

## What this does not do

- **It is not continuous.** It answers for the moment it ran. There is no CI
  in the overlay repository to schedule it - measured 2026-08-08, that
  repository has no CI at all - so today somebody runs it.
- **It is not per device.** It reports per configuration. Turning "this
  closure has CVE-X" into "these twelve laptops are exposed" needs the
  generation-to-device mapping, which the console already holds. That is the
  piece only Sextant can add, and it is not built.
- **It does not replace Wazuh, and Wazuh does not replace it.** Wazuh's own
  vulnerability detection reads a dpkg or rpm inventory that NixOS does not
  have. It therefore reports a clean fleet for every device: a false negative
  that reads as a pass. See `docs/roadmap.md` under 1.1.

## Cost

Measured 2026-08-08 on one laptop configuration (2023 components): the SBOM
took about thirteen minutes, most of it evaluating package metadata rather
than building. `vulnxscan` regenerates its own SBOM before scanning, so a run
that does both pays that cost twice. With a handful of distinct closures this
is a nightly job, not an interactive one.

**Leave disk space for the scanner.** grype downloads a vulnerability
database of a few hundred megabytes before it can do anything. On the first
run of this tool it failed with `no space left on device` after twenty
minutes of work, and the interesting part is what happened next: the run
still exited zero at the shell, and an earlier version of the script recorded
"0 findings". A full disk had produced a report saying the fleet was clean.

That is fixed - a failed scan is now recorded as `scanned: false` with a null
count, and it fails the run - but the lesson generalises past this script. Ask
of any report whether it distinguishes "we looked and found nothing" from "we
did not look".
