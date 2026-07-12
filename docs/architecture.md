# Sextant architecture

Sextant is a control-plane for fleets of NixOS devices. Configuration is data
in a git overlay repo; nix turns that data into system closures; devices pull
and converge (comin). Sextant edits the data safely, proves it builds, stages
the rollout, and reports what each device actually runs.

## Shape: hexagonal (ports and adapters)

```
transport   internal/http/web (SSR html/template, form-POST)
            internal/http/api (/api/v1 JSON: dfctl, AI, CI)
            internal/http/mw  (recover, access log, secure headers, csrf, ratelimit)
                 |
application internal/app      use-case services; one service per capability
                 |            (Config, Policy, Change, Rollout, Inventory,
                 |             Identity, Tenant)
ports       internal/ports    interfaces the app depends on
                 |
domain      internal/domain   PURE: model, scope resolver, policy compiler,
                              filter evaluator, validators. No I/O.
adapters    internal/adapters git (config repo), nix (eval gate, builder),
                              postgres (durable state), ldap, oidc,
                              integrations (netbird, wazuh, forge)
platform    internal/platform config, logging, metrics, health, server
```

Rules:
- `internal/domain` imports nothing above it. Pure functions, exhaustive tests.
- Handlers are thin: parse -> one app call -> render. No exec, git or nix in
  transport.
- Effects live behind ports; adapters are swappable and integration-tested.
- Wiring is explicit in `cmd/sextant`; each service takes only the ports it
  needs.
- Per-tenant single writer: writes to one config repo are serialized; reads
  serve an immutable snapshot.
- Nothing critical is memory-only; state rehydrates from Postgres/git on boot.

## The write path (the safety property)

Every configuration write is a transaction:

```
mutate (typed model) -> serialize -> nix eval gate -> git commit -> push
        ^ rejected edits roll back; nothing invalid ever reaches git
```

The gate forces the affected hosts' toplevel derivation, so the generator's
asserts and the NixOS module system reject unknown or mistyped options and any
injection attempt. Data can only select whitelisted option paths; it can never
carry nix code.

## Resolution model

Scope chain: organisation -> group tree (parent to child) -> device.
Policies (named setting bundles) bind to scopes via assignments, optionally
narrowed by filters over device attributes. Policy resolution compiles all
applicable contributions into one ordered chain, then one precedence rule:

- enforced: the most general scope wins (org beats group beats device); nix
  emits mkForce.
- default: the most specific scope wins (device beats group beats org); nix
  emits mkDefault.
- ties break on assignment priority, then deterministic policy order.

Every resolved value carries provenance: which policy or scope set it, and
why it won.

## Scale targets

Designed for 1,000,000 devices across tenants:
- Observed plane (check-ins) on Postgres: partitioned by (tenant, tag),
  batched upserts, read replicas for dashboards.
- Config plane shards by tenant (one overlay repo per organisation); large
  organisations split config into per-group files plus small policy objects.
- Rollout promotion reads aggregated convergence counts (GROUP BY ring);
  application code never iterates devices.

See docs/adr/ for the decisions behind this design.

## Deferred (tracked)

- Go-to-nix parity harness: lands together with the v3 overlay generator
  (resolve.nix twin). Until an overlay consumes the v3 document there is
  no second implementation to prove parity against.
- Multi-tenant runtime (model B): storage is tenant-namespaced today;
  the tenant registry and per-org mounting follow.
