# ADR 0003: Postgres is the only durable state store (besides git)

Status: accepted (2026-07-09)

## Context

The PoC stored observed state (device check-ins, inventory, stations) in
per-tenant JSON files with inconsistent locking, kept change-request
workspaces and build jobs in memory (lost on restart), and documented an
SQLite path that never existed. The platform must scale to a million devices
across tenants: at one check-in per device per minute that is roughly 17k
writes per second, far beyond file stores.

## Decision

Two stores, each with one job:
- Git holds configuration (the audit trail and source of truth).
- Postgres holds everything else durable: observed state, inventory, change
  requests, build jobs, tenant registry. Accessed via pgx, schema managed by
  versioned migrations, device_status keyed and partitioned by (tenant, tag),
  check-ins written as batched upserts.

No SQLite variant: one store implementation to test and operate. Small
installs run a Postgres container next to the binary (docker-compose does
this already); NixOS installs use services.postgresql.

## Consequences

- Restart loses nothing; state rehydrates from Postgres and git.
- HA is a deployment choice (CNPG in Kubernetes), not a code path.
- Dashboards and rollout promotion read aggregates (GROUP BY), never
  iterate devices in application code.
- Backup story: CNPG WAL archiving for Postgres, mirror for git.
