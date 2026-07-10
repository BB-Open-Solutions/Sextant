# ADR 0009: Tenant isolation by instance-per-tenant cells, centrally managed

Status: accepted (2026-07-10)

## Context

Sextant is theoretically a single point of failure and a high-value
target: it holds the keys to every customer's fleet. In-process
multi-tenancy (one app, many orgs) keeps a shared blast radius - one
authorization bug, one crash, one noisy tenant affects everyone. The
customers are audited organisations; "your data shares a process with
other customers" is a hard conversation.

## Decision

**One Sextant instance per customer (a cell), centrally managed as
declarative data.**

1. **A cell is fully private**: its own pod(s), its own CNPG database,
   its own overlay repo, its own OIDC client at the customer's IdP, its
   own secrets, its own ingress host. No shared process, no shared
   database, no shared credentials. Cross-tenant authorization bugs are
   structurally impossible because no code path crosses tenants.
2. **Central management uses the same model Sextant applies to devices**:
   cells are declarative entries in the platform GitOps repo (one
   HelmRelease per tenant), rolled out by Flux. Upgrades are staged like
   device rollouts: canary cell first, rings after. Sextant manages
   itself the way it manages laptops.
3. **Two strictly separated interfaces**:
   - The per-tenant console (this product): the organisation's own users,
     bindings, tokens, devices. Customer-facing.
   - The global admin plane: provision, upgrade, monitor and retire
     cells. Operator-facing (BB Open). It never reaches into customer
     data; it manages the cells' existence, versions and health. Starts
     as tooling over the GitOps repo plus a thin status dashboard;
     grows only when operating many cells demands it.
4. **Model B (one overlay repo per org) becomes the cell boundary**: the
   in-app tenant field remains in storage (defense in depth, honest data
   model) but 1.0 does not ship in-process multi-org routing.

## Consequences

- Per-tenant cost is one small pod plus one small database - acceptable
  for the customer profile (organisations, not freemium users).
- Upgrades are per-cell and canary-able; a bad release stops at ring 0.
- Provisioning a customer = adding declarative data to the platform repo;
  this becomes a documented, later automated, runbook (org provisioning
  capability targets the cell model, not in-process orgs).
- The evidence story improves: an auditor sees a dedicated instance,
  dedicated database, dedicated repo for their organisation.
- User management stays two-level by design: org users live in the
  customer's IdP and the cell's access list; the operator manages cells,
  never customer users.
