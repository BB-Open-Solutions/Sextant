# 0011 - Zero-touch provisioning: the station surface

Status: **console port built (2026-08-05); station migration open.**
Decided 1.0-blocking 2026-07-28 (`docs/1.0-fit-gap.md` 5b). Last of the
four Intune-parity gaps and coupled to physical hardware access.

**What this document got wrong, recorded rather than quietly edited.** It
claimed the console surface "was never ported" and that "operators are
blind". By the time it was picked up, two of the three surfaces already
existed: the discovered queue with approve-to-enroll (`/enroll`,
`enrollPage` + `postEnrollBatch`) and job progress over the full state
machine including the SB/TPM2 steps (`templates/enroll.html`, the wizard
at `/enroll/{station}/wizard`, which polls itself live via
`data-poll`). The dispatch feedback the design asks for was there too:
`postEnrollBatch` redirects into the wizard with the new jobs visible.

Only the **stations list** was genuinely missing, and the page was worse
than absent - it was a dropdown that named the stations and said nothing
about any of them. That is now a table: per station the discovered count,
jobs in flight, jobs waiting on a person, and the last report
(`stationRows` in `internal/http/web/station.go`,
`internal/http/web/station_list_test.go`).

The lesson is the same one the fit-gap just learned: a design document
that is not re-read against the code before the work starts will send
somebody to rebuild what exists.

## Problem

The provisioning *machinery* is built: the imaging domain and its
state machine (`internal/domain/imaging`, Pending → Imaging →
Installed → SB/TPM2 → Done), station report/claim APIs
(`internal/http/api/station.go`), the enrollment ceremony (design
0001) and the station-class overlay. Two things are missing:

1. **The console surface.** Stations, PXE discoveries → enroll queue
   and image-job progress were never ported from the PoC console
   (`docs/capabilities.md` #7). The APIs work;
   operators are blind.
2. **The reference station itself.** The inspoelstraat NUC still runs
   the bespoke appliance flake, outside `rings/infra`
   (`docs/station-migration-runbook.md`) - which keeps infra out of
   the rollout ladder and blocks fleet-wide rollout completion.

"Zero-touch" for a fleet device then already holds end to end: box →
PXE → image → ceremony → enrolled, no SSH. Standing up a NEW station
stays a manual runbook (`docs/handbook/src/operators/station-nuc.md`)
- acceptable at a handful of stations, same reasoning as cells
(design 0005 scope).

## Design

**Console port (the build).** Three read-mostly surfaces over the
existing domain, no new domain code expected:

- Stations list: station-class devices with their check-in state and
  active job counts.
- Discovered queue: PXE discoveries with approve-to-enroll (creates
  the image job; the one mutating action, gated + audited like every
  mutation).
- Job progress: the imaging state machine per device, including the
  SB/TPM2 wizard steps already reported through acks - one place to
  watch a device go from bare metal to Done.

UI follows the rebuilt console's patterns (cards, confirm flows); the
PoC screens are reference material, not code to salvage.

Feedback is part of the surface (operator note from the first live
inspoelronde, 2026-07-28): "start imaging" must never look like
nothing happened. The dispatch redirects straight into the job list
with the new job visible in its Pending/claimed state, and the job
row shows liveness (last runner poll, current step/progress) without
a manual refresh - the imaging state machine already reports progress,
the page just has to show it promptly.

**Station migration (operator work, runbook exists).** Migrate the
inspoelstraat NUC to the sextant-overlay per
`docs/station-migration-runbook.md`, return `rings/infra` to the
ladder. Needs physical/SSH access - user-driven, same as Push E in the
roadmap; this design only restates it as the second half of the gap.

## Explicitly not

- No zero-touch bootstrap of a NEW station (manual runbook stays).
- No multi-site station orchestration; one inspoelstraat, revisit at
  scale pain like everything else.

## Verification

Console: golden/route tests over the three surfaces against seeded
imaging state. E2e: PXE-boot the test device through the inspoelstraat,
watch discovery → approve → image → ceremony → Done entirely from the
console; then a fleet-wide rollout that completes because infra is
back in the ladder.
