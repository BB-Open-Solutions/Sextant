# ADR 0007: Built for audited organisations - approvals, segregation, evidence

Status: accepted (2026-07-10)

## Context

Sextant's customers are organisations that undergo audits (BIO, ISO
27001, ITGC-style change management). For them, "who changed what, was it
tested, who approved it" is not a nice-to-have: it is the product. These
guarantees must be structural, not bolted on.

## Decision

1. **Every change is attributable.** Mutations commit with the SSO
   identity as git author; service principals are named. No anonymous
   writes exist. The git history of the overlay repo is the primary,
   tamper-evident audit trail; the state store holds the workflow record.

2. **Segregation of duties is enforced, not advised.** The role model
   carries it: editors prepare and submit changes, only owners merge and
   start rollouts (four-eyes). A change's author being its approver is
   rejected for foundation-class changes; configurable per organisation
   for the rest.

3. **No untested change reaches devices.** The pipeline is the only path:
   eval gate on every edit, build gate on submit, optional autotest,
   staged rings with health gates on rollout. Direct writes bypass review
   but never the gates; organisations can require the change-request flow
   for chosen risk classes (foundation always).

4. **Evidence is a feature.** The console can produce an export for a
   chosen period: changes with author, approver, gates passed, build
   results, rollout outcome and affected devices - assembled from git
   history and the state store, machine-readable and human-readable.
   Auditors get evidence in minutes, not interviews.

5. **The platform itself is auditable.** Reviewed single artifact
   (image digest), reproducible builds, CI-gated merges, versioned
   migrations, no runtime plugins (ADR 0006).

## Consequences

- Approval rules become domain logic with tests, like every other rule.
- The evidence export becomes a capability on the map (reporting).
- Some friction is deliberate: foundation changes cannot skip review,
  even for owners.
