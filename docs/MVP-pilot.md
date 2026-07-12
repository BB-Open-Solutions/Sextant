# Sextant MVP — pilot-ready

The minimal capability set Sextant must deliver to run the pilots well
(Zaanstad's 6 HP laptops + the t495s canary, driven from the inspoelstraat
station). "MVP" here means *usable end-to-end on real devices*, not
production-scale (cells, 1M devices, DR) — that is post-pilot.

## Definition of done (the acceptance loop)
The MVP is done when this works end-to-end on one real device:
1. **Hang the t495s on the NUC** (inspoelstraat station).
2. **Inspoel it** — it PXE-boots, appears as *discovered* in the console,
   operator enrolls it → it gets its image + one-shot credential → active.
3. **Update it** — a config/rollout change reaches the t495s (comin) and it
   converges; the console shows the new revision.
4. **Retire / wipe it** — retire stops it (audit kept); crypto-wipe destroys
   the disk keys on the device (irreversible, per-device go).

Everything below serves this loop. The **critical path** is the t495s spine:
station/discovered plane → agent on the t495s (check-in + intent execution) →
update lands (comin-branch wiring) → retire + crypto-wipe execution. The
Integrations/Apps catalog (G) and dormant-logic activation (H) are real MVP
work but come *after* the spine proves the loop.

## Capabilities (what the app must do)

| # | Capability | State | Gap |
|---|------------|-------|-----|
| A | **Inspoelen devices** — PXE station → discovered → enrolled + one-shot credential | enroll ✅, check-in ✅ | station/discovered surface not ported to the rebuilt console |
| B | **Config management** — settings/apps/policies per scope → gated commit → converge | ✅ (gate=none live; gate-runner built) | flip gate to remote (optional for pilot) |
| C | **Add / remove** — device enroll/remove, group add/remove, user↔group | device+group ✅ | LDAP not live (users/groups) |
| D | **Update** — rollout rings, device follows pin/branch | console+engine ✅ | agent on devices + comin-branch wiring in core |
| E | **Manage / observe** — status (online/revision/drift/posture), audit, evidence, RBAC/four-eyes | ✅ | — |
| F | **Wipe / end-of-life** — retire, remote lock, crypto-wipe | retire ✅, intent-as-data ✅ | on-device execution (agent + root actd) |
| G | **Integrations/Apps catalog** — Zitadel (auth), NetBird (VPN), Wazuh (SIEM) configurable in the console | wired at deploy-time | no catalog surface; not configurable from the console yet |
| H | **Activate all built logic** — every domain capability usable end-to-end | much built | dormant/under-surfaced pieces to wire |

### Integrations are the first "apps"
The integration surface is an **Apps catalog**; the first three apps:
- **Zitadel** — auth/SSO (OIDC). Org-level config; client-secret stays a
  secret; issuer is boot-time (reload strategy or documented restart). LDAP is
  the directory source for groups here.
- **NetBird** — VPN/mesh. Config-as-data `dawo.netbird.*` (mgmt URL +
  setup-key via secret-ref, never plaintext in git) → generator emits to the
  device agent.
- **Wazuh** — SIEM. Device-side agent via config-as-data `dawo.wazuh.*` + a
  read-side census/status surface in the console.

Pattern: an integration is an app — mostly config-as-data (device agents) or
org-config (auth) — with configure / enable / status. Secrets go through a
secret-store, never `fleet.json`.

## Decisions locked (with Bram)
- **Wipe = full crypto-wipe** (destructive execution via a root actd unit that
  runs `cryptsetup luksErase`). Irreversible; careful test per device + an
  explicit go each time. Lock stays reversible.
- **Integrations configurable in the console** (not only env/helm/nix):
  - NetBird/VPN as **config-as-data** — `dawo.netbird.*` (mgmt URL, setup-key
    via secret-ref, never plaintext in git) surfaced in the catalog/settings,
    the generator emits it to the device.
  - SSO/OIDC and LDAP as **org integration settings** in the console; the
    bind-password / client-secret stay secrets. OIDC issuer is boot-time —
    needs a reload strategy or a documented restart; the LDAP adapter is
    per-request, so runtime-reloadable.
- **Gate**: gate-runner built + deployed (observe). Flip to `remote` once a
  test `/validate` against the real overlay is green; otherwise stay `none`
  for the pilot (CI + parity harness remain the gate).
- **Design fidelity**: work from the live Stitch source (MCP) for new
  surfaces (station, integrations), not the static zip.

## Roadmap (ordered toward pilot-ready)

**M-pilot-1 — Integration config surfaces** *(task #44)*
NetBird/VPN + SSO/OIDC + LDAP configurable in the console. Unblocks C and G;
pull the connectivity/integration screens from Stitch MCP.

**M-pilot-2 — Inspoelstraat plane** *(task #43)*
Station reports → "discovered" device state → one-click enroll-from-discovered.
Closes A.

**M-pilot-3 — Live on real devices** *(tasks #45, #46)*
Rust agent on the HPs + t495s (check-in/posture/intent-ack); comin-branch
wiring in DAWO-NixOS/overlay so ring-promotion actually lands. Closes D, makes
E's posture live.

**M-pilot-4 — End-of-life** *(task #45)*
Retire + remote lock (reversible) executed on-device, then the destructive
crypto-wipe (root actd, tested, per-device go). Closes F.

**M-pilot-5 — LDAP / users live** *(tasks #29, #44)*
Directory endpoint + read-only bind creds (secret) so groups/users resolve.
Closes C's user↔group.

**M-pilot-6 — Activate all built logic** *(task #47)*
Capability→surface audit; wire the dormant pieces (risk-acceptance,
service-account management, …) so nothing built sits unused. Closes H.

**Cross-cutting — gate flip** *(task #37)*: verify runner, flip when green.

## Engineering bar (non-negotiable — ships to code.overheid.nl)
Every batch is held to this, during the build, not just in the #48 audit:
- **Security first**: secrets never in git (setup-keys, bind-passwords,
  client-secrets go through env/agenix/secret-refs); injection firewall on
  every new input; RBAC owner-only on integration/admin config; new ingestion
  endpoints (station report) authenticated with a scoped credential; CSP clean;
  fail-closed on the gate/destructive paths.
- **Tests, a lot of them**: domain unit tests, adapter integration tests
  (testcontainers/ephemeral PG), HTTP tests per route (RBAC/CSRF/authz cases),
  contract tests for `/api/v1`. Nothing ships without tests around it.
- **Error logging**: structured `slog` with context, no swallowed errors; every
  failure path logs actionably; readiness reflects real dependency health.
- **No god-files, no AI slop**: small cohesive files, clean layering, idiom of
  the surrounding code, no dead/speculative helpers, human-led commit language.

## Not in the pilot MVP (post-pilot)
Cells / multi-tenant admin plane (ADR 0009), 1M-device scale hardening,
backup/DR drills, load tests, public code.overheid release (#19, needs go).
