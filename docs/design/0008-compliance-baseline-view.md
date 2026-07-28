# 0008 - Compliance baseline view

Status: draft for review. Decided 1.0-blocking 2026-07-28
(`docs/1.0-fit-gap.md` 5b). Presentation work only - no agent change,
no schema change, no new subsystem.

## Problem

Every ingredient of "does this device meet the baseline" is already
collected - SB/TPM2 posture (`internal/domain/observed/posture.go`),
check-in recency, running revision, profile drift
(`internal/http/web/policies_page.go`) - but the console never adds
them up. A municipal audit ("show me which devices are compliant")
today means reading four surfaces per device. Intune ships this as
compliance policies + reports; we can ship the report for free.

## Design

One computed verdict per device, derived at render time from state we
already hold. No stored compliance state, no policy engine: the
baseline is opinionated and fixed, like the rollout ladder.

A device is **compliant** when all of:

1. Checked in recently (within 3x the agent beat interval).
2. No profile drift (its policy stamps match the overlay's current
   profiles - the `reapply` state from `profileState` is absent).
3. Disk encryption posture is sound for its class: SB `enforcing` +
   TPM2 `enrolled` for wizard-class devices; stations and classes
   without the ceremony are exempt from this criterion, not from the
   others.
4. Running revision is current for its ring (the ring's promoted
   revision, not necessarily the newest release - a device mid-ladder
   is compliant, a device behind its own ring is not).

Anything else is **needs attention**, listing exactly which criteria
fail. Two states only - a traffic light with amber invites debate a
baseline should not have.

Surfaces:

- Devices overview: verdict column + filter, count in the header.
- Device page: the verdict with its failing criteria spelled out.
- Export: CSV of the devices table including the verdict and the
  per-criterion values, audited like other reads of fleet state. This
  is the audit artifact.

## Explicitly not

- No configurable criteria per org (revisit if a real tenant needs it).
- No conditional access / enforcement - the verdict informs, the
  rollout engine already enforces convergence.
- The BIO comply-or-explain register stays deliberately later
  (`docs/capabilities.md` #8); the acceptance register in the domain is
  untouched.

## Verification

Unit tests over the verdict function (each criterion independently
failing); golden test for the CSV; e2e: flip one criterion on t495s
(stop the agent, watch recency fail) and see the verdict move.
