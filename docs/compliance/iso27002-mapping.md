# What Sextant covers, and what the municipality still has to do

Status: **draft, 2026-08-07.** Written against the running product; every
"covered" row below points at a mechanism that exists and was measured, not
at a roadmap item.

## Read this first

Two warnings, because a mapping document is exactly the kind of artefact that
gets quoted out of context in a tender.

1. **Control numbers here are ISO 27002:2022.** The BIO builds on that
   standard but has its own numbering and its own additional requirements at
   BBN levels. Nobody on this side has held this table against the BIO text
   itself. **Do not quote a BIO number out of this document** until somebody
   with the BIO in front of them has walked it. Inventing a plausible number
   is worse than leaving it blank, because a number reads as verified.

2. **A tool never satisfies a control on its own.** Every row below is at
   most "the technical part is available and enforced". The policy, the
   assignment of responsibility, the review cadence and the evidence keeping
   are the municipality's, and most rows need all four.

## Covered by the product, and enforced

| Control (ISO 27002:2022) | What Sextant does | Where |
|---|---|---|
| 5.15 Access control | Roles resolved per request from IdP groups; Viewer/Editor/Owner, scoped to org, group or device | ADR 0008, `internal/domain/identity` |
| 5.16 Identity management | One SSO authority per deployment; the directory is the source of truth | ADR 0015 |
| 5.17 Authentication information | Tokens hashed with argon2id, constant-time compare, mandatory TTL, per-device credentials bound to their tag | `internal/domain/token`, `internal/app/cred.go` |
| 5.18 Access rights | Group-derived, re-evaluated per request; a deleted group loses its rights immediately | `internal/app/token.go:149` |
| 8.2 Privileged access rights | Elevation requests: a user asks, an operator decides, both sides recorded | #27, `internal/app/elevation.go` |
| 8.5 Secure authentication | OIDC for the console; device login through SSSD against the directory | ADR 0015, ADR 0021 |
| 8.7 Malware protection | Wazuh agent enrolment per group (the manager is the customer's) | `dawo.wazuh.*` |
| 8.8 Vulnerability management | **PARTIAL** - the update chain delivers fixes, but nothing reports which CVEs a fleet is exposed to. See the gap list |
| 8.9 Configuration management | The whole product. Config as data, reviewed, gated, versioned in git, and every device converges on it | ADR 0005, ADR 0012 |
| 8.10 Information deletion | **PARTIAL** - remote wipe with three independent walls; diagnostics expire in 14 days; but see the retention gaps in the processing register |
| 8.11 Data masking | Secrets are references or sealed values; plaintext never enters the fleet document | ADR 0018 |
| 8.12 Data leakage prevention | USB device control with allowlist (opt-in per group) | `dawo.usbControl` |
| 8.15 Logging | Every configuration change is a git commit with author, time and reason; the console keeps an audit tail | `internal/adapters/git` |
| 8.16 Monitoring activities | Device check-ins, health and drift; incidents raised for silent, failing or lagging devices | `internal/domain/incident` |
| 8.19 Software on operational systems | Only what the fleet document declares; a device that cannot evaluate its config refuses to switch | ADR 0005 |
| 8.20 Network security | Cluster services reachable over the WireGuard mesh; nothing published that need not be | `dawo.netbird.*` |
| 8.24 Cryptography | Full-disk encryption with TPM2 unlock and escrowed recovery keys; secrets sealed at rest | design 0009 |
| 8.31 Separation of environments | Cells: one deployment per customer, per environment | ADR 0009 |
| 8.32 Change management | Change requests, four-eyes, a gate that must pass before merge, staged rollout with soak and health thresholds | ADR 0012, delivery-process |

## Where the product helps but does not deliver the control

| Control | What is missing |
|---|---|
| 5.9 Inventory of information and assets | Device inventory is real and current, but it is a DEVICE inventory. Information assets - what data lives where - is not something a fleet tool knows |
| 5.30 ICT readiness for continuity | Boot-health rollback protects a device; the console's own continuity is thin. The observed plane's backup shipped in 0.85.0 and **has never been restored** (`docs/runbooks/restore-observed-plane.md`) |
| 8.13 Information backup | The product backs up its OWN database, not the user data on devices. Nothing in Sextant backs up a laptop |
| 8.16 Monitoring | Sextant watches configuration convergence, not security events. Wazuh is where that lives, and its manager is the customer's to run |

## Not covered, and honestly outside the tool

Every organizational control (5.1-5.8, 5.19-5.29, 5.31-5.37), everything
people-related (6.x: screening, terms of employment, awareness, disciplinary
process, post-employment), and everything physical (7.x). A fleet tool has no
opinion on any of it.

The one worth naming explicitly because customers ask: **5.7 Threat
intelligence** and **8.8 Vulnerability management**. A municipality moving
from Intune expects a CVE view, and there is none. `docs/1.0-fit-gap.md`
records it as a known gap against the competition; here it is again as a
control gap, because that is where it will be raised in an audit.

## Open gaps that are the product's own

These are not "outside the tool" - they are things Sextant should do and does
not:

1. **No CVE or vulnerability reporting** (8.8). The single most-asked-for
   thing in this list.
2. **No retention on most personal data** (8.10, and GDPR art. 5). One
   window exists, out of ten processings. See the processing register.
3. **No erasure path** for a departed employee.
4. **The restore procedure has never been run** (5.30). A recovery path
   nobody has walked is an assumption, and this one holds the LUKS recovery
   keys of every device.
5. **Device login over plain LDAP** at bb-open (8.24, 8.5). Guarded and
   acknowledged since ADR 0021, but still live. Audit finding H3.
6. **The console pushes as a person, not a machine** (5.16, 8.15). The
   mechanism to fix it shipped in 0.85.0; the machine account does not exist.
   Audit finding H2.

## How to use this

For a tender or an audit, take the first table as claims to be checked, the
second and third as scope boundaries, and the fourth as the honest defect
list. A mapping that shows only the first table is marketing, and an auditor
will find the fourth anyway.

The policy mechanism that carries control annotations per policy already
exists (`Controls []string`, `internal/domain/fleet/model.go:257`) and
appears in the CSV export. Measured on 2026-08-07: **zero policies are
defined in production**, so the mechanism has never carried a real
annotation. Wiring these mappings into actual policies is the step that turns
this document into evidence rather than prose.
