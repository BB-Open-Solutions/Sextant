# Multi-tenancy: in-process model B

Status: exploratory - superseded for 1.0 by ADR 0009 (see the note below).
Written to document the actual state of the code against the rebuild plan's
"model B" and to leave a build-ready design if model B is ever revisited.

## 1. Goal and model B recap

The rebuild plan's model B: several customer organisations running inside
**one** Sextant deployment. Each tenant gets its own overlay git repo
(fleet.json + catalog) as config source of truth, isolated observed-plane
storage, safe tenant-switching with no cross-tenant pivot, and runtime
org-provisioning from the console UI - a new customer is a UI action, not a
deploy.

**This conflicts with a decision already made in this repo.** ADR 0009
(accepted 2026-07-10, six days before this document) chose the opposite
topology: **one Sextant instance per customer (a cell)**, centrally managed
as declarative GitOps data, specifically *because* in-process multi-tenancy
keeps a shared blast radius - "one authorization bug, one crash, one noisy
tenant affects everyone" (`docs/adr/0009-tenant-isolation-cells.md`). The ADR
says explicitly: *"Model B (one overlay repo per org) becomes the cell
boundary [...] 1.0 does not ship in-process multi-org routing."*
`docs/design/0005-cells-provisioning-admin-plane.md` is already written and
marked "ready to build" as ADR 0009's execution plan - a template overlay
repo, one K8s Secret and one HelmRelease per tenant, rolled out by Flux.

So: this document answers "how would model B work" honestly, against the
real code, but it is not the roadmap. Section 6 asks whether it should stay
filed as a rejected alternative or has a live reason to be revived (e.g. a
customer segment too small to justify a dedicated pod + database).

## 2. Factual state per plane

The storage layer is tenant-*shaped* almost everywhere; the runtime wiring
is single-tenant everywhere. One constant makes this literal:

```go
// internal/app/inventory.go:14
// DefaultTenant names the single-tenant namespace until multi-tenant
// routing lands (phase 5+); the storage schema is tenant-ready today.
const DefaultTenant = "default"
```

| Plane | State | Evidence |
|---|---|---|
| **Config** | Single-tenant, hardwired | `internal/app/config.go`: `ConfigService` holds one `repo ports.ConfigRepo` field and one `writeMu sync.Mutex` for the process's lifetime - no tenant parameter anywhere in the type. `cmd/sextant/capabilities.go:133` constructs exactly one `svc` for the whole process. `internal/adapters/git/git.go`: `Repo` wraps one `dir string`; nothing tenant-aware. |
| **Observed** | Tenant-ready storage, single-tenant runtime | Every Postgres table takes `tenant` as a real column and query parameter: `device_status`/`device_facts` (`internal/adapters/postgres/postgres.go`), `discovered` (`discovered.go`), `device_secrets` (`device_secrets.go`), `smtp_config` (`smtp.go`), `notifications`/`notification_reads` (`notify.go`), `user_prefs` (`prefs.go`). But every service constructor is called with the literal `app.DefaultTenant` at startup (`cmd/sextant/capabilities.go:179-214`) - one process, one tenant value, for the process's lifetime. |
| **Identity** | Single-tenant, hardwired | `internal/adapters/oidc/oidc.go`: one `ClientID`/`ClientSecret` pair against one Zitadel org, one static `OIDCRedirectURL` (`internal/platform/config/config.go:221`). `internal/adapters/ldap`: one bind account, one directory. No org-claim, no per-tenant IdP selection. |
| **Gate / cache** | No tenant concept at all | Zero hits for "tenant" in `cmd/gate-runner`, `internal/adapters/gate`, `internal/adapters/nix`. The gate validates whatever `repo.Dir()` it is handed; the signed release cache (`cfg.GateURL`/`cfg.GateToken`, wired in `capabilities.go:253`) is one shared endpoint and one shared signing key. |
| **UI / session** | Single-tenant, hardwired down to the handler | `internal/http/web/web.go`: the `Sessions` interface (`SessionUser`) returns a user and a CSRF token - no tenant. Handlers call `app.DefaultTenant` directly inline (`web.go:351`, `web.go:364`) rather than reading it from anything request-scoped. `orgName` is a single string field set once (`SetOrgName`, `web.go:93`). The RBAC scope chain is rooted at one organisation by construction (`docs/architecture.md`: "organisation -> group tree -> device") - there is exactly one root per process, not per request. |

Honest summary: the observed plane was built tenant-namespaced from the
start (defense in depth, per ADR 0009 point 4 - "the in-app tenant field
remains in storage"), but nothing above the Postgres row - not the config
repo, not identity, not the gate, not sessions - carries a tenant identifier
today. Multi-tenancy is a storage-schema head start, not a running feature.

## 3. Design (if model B is built)

### Tenant registry

Durable in Postgres, not git: a new `tenants` table (id, slug, display
name, status, created, IdP config reference, overlay repo URL + deploy
credential reference, quota). Git overlay repos are per-tenant *config*
data; the registry is relational metadata that needs listing, joins to
usage/quota, and status transitions - it belongs next to the other durable
control tables the observed plane already uses (`internal/adapters/postgres`
already owns migrations). The cell model's equivalent is one HelmRelease
per tenant in the platform GitOps repo (design 0005); model B collapses
that into one table plus a lazily-materialized repo clone.

### ConfigService-per-tenant

Today: one `*app.ConfigService` bound to one on-disk clone for the process's
life (`internal/app/config.go`). Multi-tenant needs an **LRU-bounded pool**
keyed by tenant ID, each entry a `(repo clone, ConfigService, writeMu)`
triple:

- Idle tenants are evicted after N minutes; a write or read re-clones on
  demand (the sync/remote machinery already exists: `repo.HasRemote()`,
  `repo.Sync()` in `config.go`).
- **Critical invariant**: `writeMu` must be per-tenant. A pool keyed by
  tenant ID gives this for free as long as no code path shares one
  `ConfigService` instance across tenants; a single global lock instead of
  a per-tenant one would let one tenant's write serialize behind another's
  - an availability leak across the tenant boundary, not just a data one.
- Memory cost is a git working tree per *active* tenant (single-digit MBs);
  bounded by the LRU, not by tenant count.

### Gate-runner and release cache: shared pool, hard per-tenant namespace

Recommendation: **shared worker pool, per-tenant quota; never a shared
signing key.**

- The gate-runner is already memory-bounded per batch, not per fleet (see
  `docs/handbook/src/architecture/scale.md`), so sharing the pool across
  tenants doesn't reopen the OOM problem that batching solved. Add a
  per-tenant token-bucket on top so one tenant's chunk-parallel run can't
  starve another's - the same "noisy tenant" risk ADR 0009 names, scoped
  down to a rate limit instead of a whole instance.
- The signed release cache must **not** share a signing key across
  tenants. A shared key means a compromised or malicious tenant's build
  pipeline can forge cache entries every other tenant's devices trust -
  a supply-chain hole, strictly worse than an authorization bug. Each
  tenant gets its own object-store prefix (or bucket) and its own signing
  keypair; only the physical infrastructure (MinIO/Garage cluster) is
  shared.

### Identity: per-tenant OIDC client, not shared-IdP-with-org-claim

Recommendation: **register a distinct OIDC client per tenant against that
tenant's own IdP**, mirroring what ADR 0009 already committed to per cell
("own OIDC client at the customer's IdP"). Reasons this still holds
in-process:

- The customer base is audited organisations, each typically with its own
  IdP; a shared-IdP model forces them onto BB Open's Zitadel instead.
- An org-claim model makes Sextant's own claim-to-tenant mapping code the
  *entire* isolation boundary - exactly the "in-process authorization bug"
  class ADR 0009 was written to eliminate. A missing or wrong claim check
  is a direct cross-tenant login.
- If a shared IdP is unavoidable (e.g. small customers hosted on BB Open's
  own Zitadel), the org-claim must be checked server-side against the
  session's resolved tenant on every request, not only used to render the
  UI - belt and suspenders, but per-tenant client stays the default.

### Ingress/URL model: subdomain per tenant

Recommendation: **subdomain (`tenant.sextant.example`), not path
(`sextant.example/tenant/`)**.

- Cookies are Host-scoped for free on a subdomain; under one origin with
  path-based routing, a single bug in the path-parsing layer (CSRF token,
  session cookie, redirect) becomes a same-origin cross-tenant leak - the
  one class of bug this whole design exists to prevent.
- It matches the cell model's already-decided "own ingress host" per
  tenant (ADR 0009), reusing the same cert-issuance pattern already live
  for `docs.sextant.bb-open.com` (ClouDNS + cert-manager).
- The OIDC redirect URL is a single static config value today
  (`config.go:221`); subdomain-per-tenant needs either wildcard redirect
  validation or (preferred, consistent with the identity recommendation
  above) a per-tenant OIDC client with its own registered `redirect_uri`.

### Secret isolation per tenant

Today one `secretbox.Sealer` (`internal/platform/secretbox`) is wired once
at startup and shared by every tenant-scoped secret row (device secrets,
SMTP passwords). The `tenant` column is the only thing separating rows; the
encryption key is common to all of them, so a missing `WHERE tenant = $1`
bug is not just a logic error but an actual decrypt of another tenant's
secret. Recommendation: **envelope encryption with a per-tenant data key**,
wrapped by one master key. Ciphertext from tenant A is then cryptographically
inert to tenant B even if the row-scoping bug ships - isolation becomes
defense in depth rather than a single SQL clause away from a leak.

## 4. Blast-radius / security invariants

- **Session -> tenant binding.** A session must resolve to exactly one
  tenant at issuance and never take a tenant from client input (URL, form
  field, header). `web.go`'s inline `app.DefaultTenant` is this pattern
  done correctly for N=1; generalizing it means `session.TenantID`, always
  server-derived, never client-supplied.
- **No live tenant-switch inside one session.** If cross-org support staff
  is ever a feature, switching tenants must force a new authentication
  event against the target tenant's IdP, never a client-side selector that
  silently reinterprets an existing session/CSRF pair - that is precisely
  the "in-process pivot" ADR 0009 exists to close off.
- **Structural store namespacing, not conventional.** Every tenant-scoped
  query already takes `tenant` as its first parameter (see the table in
  section 2), but each is a handwritten SQL string with no compiler check
  that the clause is present. A `TenantScope` wrapper type that stores can
  only be reached through would turn "forgot the WHERE" into a build
  error instead of a runtime cross-tenant read.
- **Repo credentials never share a keyspace coarser than tenant ID.** Each
  tenant's git deploy credential must be scoped to that tenant's repo only
  (ADR 0009 already requires this per cell). In-process, this means N
  distinct credentials live in one process's memory concurrently -
  they must never sit in a store keyed by anything less specific than
  tenant ID, and must be wiped on pool eviction (ties to the ConfigService
  LRU above).
- **Fail-closed stays per-tenant-request.** The gate is fail-closed
  cluster-wide today (`docs/handbook/src/architecture/scale.md`,
  Availability). In a shared multi-tenant pool this property must hold
  per request: one tenant's malformed catalog or misbehaving job must not
  degrade gate availability for any other tenant.

## 5. Phasing (if resumed)

1. **Tenant registry + session binding.** `tenants` table in Postgres;
   generalize the `DefaultTenant` threading already present in every
   Postgres adapter into a real `session.TenantID`. No behavior change at
   N=1 - this only makes the current single-tenant seam explicit and
   testable.
2. **ConfigService-per-tenant pool.** Replace the single global `svc` in
   `cmd/sextant/capabilities.go:133` with the LRU pool from section 3;
   runtime provisioning creates a registry row and clones the tenant's
   overlay repo from a template on first use.
3. **Per-tenant identity + secret isolation.** Per-tenant OIDC client
   wiring, replacing the single shared `oidc.Config`; envelope-encrypted
   per-tenant data keys, replacing the single shared `secretbox.Sealer`.
4. **Per-tenant ingress + gate/cache hardening.** Subdomain routing,
   per-tenant signing keys on the release cache, per-tenant rate limits on
   the shared gate-runner pool.

Each slice is independently shippable and each one only matters if model B
is actually the target topology - see section 6 before starting slice 1.

## 6. Open questions for Bram

1. ADR 0009 already chose cells over model B, and design 0005 is "ready to
   build" as its execution. Should this document be filed as a rejected
   alternative for the record, or is there a concrete reason to keep model
   B alive (e.g. a customer segment too small/short-lived to justify a
   dedicated pod + database per ADR 0009's cost model)?
2. If model B stays alive: is it a second supported topology chosen per
   customer, or a stepping stone meant to be deleted once cells ship?
3. Runtime org-provisioning from the console UI (as scoped in this
   request) conflicts with ADR 0009's "operator plane never reaches into
   customer data" boundary and design 0005's declarative-GitOps
   provisioning story. If self-service provisioning is still wanted, which
   plane owns it - the global admin plane, or a genuinely new capability?
4. The identity recommendation (per-tenant OIDC client) assumes each
   tenant brings its own IdP, matching ADR 0009. Is there a real case for
   many small customers sharing BB Open's own Zitadel instead, which would
   push the design toward org-claim isolation and its extra trust burden?
5. Subdomain-per-tenant needs wildcard TLS/DNS or per-tenant cert
   automation. Does the existing ClouDNS + cert-manager flow support
   wildcard issuance today, or would each tenant subdomain need a manual
   cert step?
