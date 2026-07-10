# Sextant roadmap to 1.0

The definition of done for a 1.0 an audited enterprise can adopt:
IdP-agnostic identity, a config plane that is the source of truth (not a
pod volume), a real agent, scoped credentials everywhere, a stable
documented API, and evidence on demand. Grouped into work streams, then
sequenced into milestones.

Status legend: [x] done, [~] partial, [ ] not started.

## Self-audit findings folded in (gaps beyond the known list)

- [x] Config repo is a pod PVC = single point of failure. Git remote
      (Forgejo) is the source of truth in production (init clone +
      sync loop + push).
- [x] External commits to the overlay are not picked up (snapshot only
      refreshes on the console's own writes/merges). Need a sync loop or
      forge webhook, with conflict handling.
- [x] `dfctl` is a stub - the "API/CLI-first" claim needs a real CLI.
- [x] No diff view: an approver cannot see what a change alters before
      merge. Git has it; API and UI must surface it.
- [x] Entra groups "overage": Graph transitiveMemberOf fallback with
      paging (M2.3).
- [x] No OpenAPI spec or contract tests - required for a frozen,
      documented, future-proof API.
- [x] The agent is one designed component: the Rust sextant-agent
      (ADR 0010) - check-in, facts, retirement handling, nix module.

## Work streams

### A. Identity and IAM (IdP-agnostic)
- [x] OIDC + PKCE, encrypted sessions, per-scope RBAC
- [x] Claim shapes: Keycloak arrays, Zitadel roles map, Entra app roles
- [x] Entra/AD groups overage handling (Graph fallback)
- [x] Group discovery/browse via the optional LDAP directory port
      (Zitadel = login, LDAP = groups; free text remains without it)
- [ ] LDAP/AD direct bind as an auth source (not only OIDC) where required
- [ ] SCIM inbound (optional) for user/group provisioning from the IdP
- [ ] Device login story documented per provider (SSSD/Kerberos in image)

### B. Config plane as source of truth
- [x] Overlay in a git remote (production: sextant-overlay-bbopen on
      Forgejo; init clone, sync loop, HA push)
- [x] Sync loop so external commits refresh the snapshot (30s, write-locked)
- [x] The v3 nix generator + `resolve.nix` twin + parity harness in CI
      (nix/ in this repo; overlays import via the sextant flake input)
- [~] gate=eval proven end to end (examples/overlay + the production
      overlay v3 flake evaluates); the prod flip needs the gate-runner
      decision (nix in the console image vs a sandboxed runner)
- [x] Catalog export (exportCatalog + exportCatalogFromOptions over an
      evaluated host); production catalog.json carries the real core
      options with defaults and riskClass
- [x] Diff view (API + UI): what a change alters, before merge
- [ ] Backup/restore drill documented (git mirror + CNPG WAL)

### C. Settings and capability surface (Odoo model, ADR 0005/0006)
- [x] Capability registry refactor (main wiring -> registry)
- [x] Generic catalog renderer (type -> widget, category, risk class)
- [x] Settings editor per scope with enforce/lock toggle
- [x] Policy/assignment/filter editors in the console
- [x] App lists per scope (packages/flatpaks/overlays as data)
- [ ] Foundation tier UX (owner-only, change-request-required)
- [x] Localization: EN/NL message catalog and per-user timezone
      rendering (org defaults as fallback)

### D. Lifecycle, agent and provisioning
- [x] Device lifecycle states (active/retired; retire = audited commit,
      credential revoke, 410 on check-in, no host in the generator)
- [x] The agent as one designed component: Rust sextant-agent (check-in,
      facter inventory, retirement, jitter; flake package + nix module)
- [x] Per-device credentials issued at enrollment (ADR 0008)
- [ ] Inspoelstraat plane: stations, PXE discoveries -> enroll queue,
      image builds (port + redesign from the PoC)
- [ ] Remote actions: lock, cryptographic wipe, retire (declarative)
- [x] Update funnel: machine-owned rings/<group> branches steered by the
      rollout engine (ADR 0011)

### E. Assurance and security (ADR 0007/0008)
- [x] Four-eyes at merge (owner), gated pipeline
- [x] Personal API tokens acting as their user + service accounts
- [x] Segregation of duties enforced (four-eyes: author != approver)
- [ ] Evidence export (period -> who/what/tested/approved/rolled out)
- [ ] Threat model doc + security-review gate in CI
- [ ] Secret handling review (rotation, no plaintext at rest) end to end

### F. Platform hardening and future-proofing
- [x] OpenAPI spec published (/api/v1/openapi.json); contract tests both
      directions; additive-only policy stated in the spec
- [x] CI runs the suite on push (Forgejo Actions): Go + agent + nix,
      coverage gate 70%
- [ ] Observability: metrics dashboards, tracing, deep readiness per dep
- [ ] Multi-tenant = cells (ADR 0009): per-customer instance provisioning
      runbook + tooling over the platform GitOps repo; thin global admin
      status dashboard; canary-ring upgrades across cells
- [ ] Schema/version migration story for fleet.json (v3 -> v4 ...)
- [ ] Load test to the stated scale target; document limits
- [ ] Docs: operator runbook, admin guide, per-IdP integration guides

## Milestones

**M1 - Trustworthy core.** B (git-source-of-truth, sync, v3 generator,
catalog export, diff), F (CI runs suite, OpenAPI). Without this the
config plane is not real; everything else builds on it.

**M2 - Enterprise identity + credentials.** A (overage, group browse,
LDAP/AD, device-login docs), E (personal/service tokens, per-device
creds, SoD). Makes it adoptable by an audited org on their IdP.

**M3 - Full management surface.** C (registry, catalog renderer, editors,
app catalog), D (lifecycle, agent redesign, update funnel). The
Intune-parity daily-management experience.

**M4 - Scale + provisioning + evidence.** D (inspoelstraat, remote
actions), E (evidence export, threat model), F (cell provisioning +
admin plane, load test, migrations, docs). 1.0: enterprise-ready,
auditable, future-proof.

## Future-proofing principles (non-negotiable through 1.0)

- Data-not-code boundary holds (ADR 0005); the catalog is the contract.
- One authorization path for humans, tokens, machines (ADR 0008).
- Every change auditable and reversible via git; no imperative device RCE.
- API additive-only after v1 freeze; schema migrations versioned.
- One reviewed artifact, no runtime plugins (ADR 0006).
- Each capability: spec + ADR, pure domain + tests, ports, then UI.
