# Design 0005: cell provisioning + thin admin plane (ADR 0009 execution)

Status: designed 2026-07-27; provisioning tooling in build (Push A),
admin plane follows as its own push (Push B).

## Problem

ADR 0009 chose instance-per-tenant cells. Today provisioning a cell is
hand work (helmrelease + secrets + overlay repo). 1.0 needs: a runbook
that is mostly `git commit`, and a thin global view over all cells.

## Guiding scope decision: manual over machinery

Provisioning happens a few times per year at current scale. Every
automation candidate was weighed against what it replaces:

- **No forge driver / provisioning CLI.** Creating the overlay repo from
  the template, adding a deploy key and minting a repo-limited token is
  a few minutes in the Forgejo UI. A Go driver + CLI would introduce a
  standing provisioning credential (a new trust concentration) to save
  those minutes. Revisit when cell count makes clicking painful
  (roughly >5 cells, or a first operator outside BB Open).
- **No scaffolder script.** The per-cell GitOps files come from a
  template directory (`apps/_sextant-cell-template/` in the platform
  repo): `cp -r` + `sed` + a short checklist. The manual review of the
  resulting diff before commit is a feature, not a gap; the existing
  `validate-manifests.sh` is the mechanical check.

The runbook (platform repo, `docs/runbooks/new-sextant-cell.md`) is the
executable form of this design.

## A cell is four artifacts, all declarative

### 1. Overlay repo on the forge

From the template repo `sextant-overlay-template` (created once, marked
"template" in Forgejo; new cell = "Use this template" button). Content,
modelled on `examples/overlay/` and the bb-open overlay:

- `flake.nix` - inputs `sextant` + the DAWO-NixOS core; outputs
  `nixosConfigurations` via the generator and the `catalog` export.
- `fleet.json` - minimal valid: `{"version": 3}` (every other field is
  optional; the console's forward-migration seam handles the rest).
- `catalog.json` + `regen-catalog.sh` (with `--check` drift mode).
- `hardware-profiles.json`, `profiles.json`, `bundles.json` - optional
  at runtime; ship empty-but-valid.
- `overlays/.gitkeep` (ADR 0014), `README.md`, `flake.lock`.

The template carries **no secret, no cache signing key, no
cell-specific host**. Secrets live age-encrypted per cell or in the
cluster Secret, never in the template lineage.

### 2. Secrets (the only imperative cluster state)

One K8s Secret per cell (`sextant-<org>`, SEXTANT_* keys) plus the git
credential secret (`sextant-<org>-overlay-netrc`) and, when the release
cache is on, the cache signing key. Never in git; provisioned via
kubectl per the runbook's key table. Two deploy credentials per cell,
both issued in the forge UI:

- **device-read**: a repo-scoped READ-ONLY access token, pulled over the
  forge's public HTTPS ingress (the Intune model: the update channel is
  always public TLS + a credential, never VPN- or SSH-dependent —
  revised 2026-07-27 after finding the forge's SSH is cluster-internal
  only). The token reaches devices as an agenix secret; comin's native
  `remotes[].auth.access_token_path` consumes it
  (overlay `sextant.console.overlayTokenSecret`). Revocable and
  rotatable per fleet; per-device credentials for git would need a
  token-issuing proxy - MSP-phase work.
- **console-write**: a repository-limited access token (Forgejo:
  token restricted to exactly one repo, scope `write:repository`) on a
  per-cell machine account `sextant-<org>`. The console commits `main`
  and force-pushes `rings/*` (ADR 0011); repo-limiting the token turns
  "stolen console credential" into "write access to that one cell's
  overlay" - a real narrowing of threat-model R4.

The cell runs without a shared check-in token: per-device credentials
authorize check-in on their own, so `SEXTANT_CHECKIN_TOKEN` is simply
omitted (closes threat-model R2 for every new cell by construction).

### 3. HelmRelease in the platform GitOps repo

`apps/sextant-<org>/helmrelease.yaml` (NOT `tenants/` - matches the two
cells already shipping). Values split in two kinds; the split is what
the template directory encodes:

| Cell-invariant (template keeps) | Per-cell (placeholder/checklist) |
|---|---|
| chart sourceRef (shared GitRepository `sextant`) | `orgName`, ingress `host` |
| image repository, pull secret | image `tag` (= prod tag at birth) |
| `className: cilium`, cert-manager issuer | `oidc` issuer/clientId/scopes |
| resources, storageClass defaults | `gitRemote.url` (overlay repo) |
| `gateMode: remote` + gate-runner block | `secretName`, `netrcSecret` |
| remediation, intervals | `cnpg.name`, `ownerGroups`, locale |

`gate: remote` is the default and fail-closed; `gate: none` requires
the explicit `allowUnvalidated` acknowledgement (the app refuses
otherwise). `--owner-groups` is mandatory in the checklist: a cell
without it has no reachable administrator.

### 4. Flux Kustomization wiring

`clusters/proxy-nbg2-prod/sextant-<org>.yaml`: path
`./apps/sextant-<org>`, `prune: true`, `wait: false`, `dependsOn:
cert-manager-issuers`. Retirement is deleting this file plus the app
directory; Flux prunes the namespace and everything in it.

New cells also carry the NetworkPolicy set (default-deny + DNS +
intra-namespace console/gate-runner/CNPG + ingress-controller +
monitoring scrape + egress 443), satisfying the platform's ADR-0013
check from birth. The existing cells predate that check; the set is
canaried on `sextant-demo` first and prod is retrofitted in its own
change window.

## A cell without our platform (third-party install)

Easy installation for others is a product property (ADR 0001 lineage:
sovereign, no vendor coupling). A cell needs exactly:

1. the Helm chart from this repo (`deploy/helm`) - plain `helm install`
   works; Flux is our convenience, not a requirement;
2. a git repo on ANY forge for the overlay (runtime git is plain git
   over netrc/ssh - GitHub, GitLab, Codeberg, Forgejo all work), started
   from `examples/overlay/`;
3. an OIDC issuer (any standard OIDC + a group claim, ADR 0015);
4. Postgres (the chart's CNPG block, or bring-your-own DSN);
5. the SEXTANT_* secrets.

The template repo, template directory and runbook are BB Open
conveniences layered on top. Nothing in the product depends on them,
and this design adds no code to the app.

## Identity per cell (ADR 0015)

The OIDC issuer, client and group-claim mapping are per-cell values.
Two first-class paths: BB Open provisions a client at its Zitadel, or
the customer brings their IdP (Keycloak/Nubus/Entra/ADFS - standard
OIDC + groups claim). The console derives its redirect URL from the
ingress host; the IdP client must allow `https://<host>/callback`.
Exactly one SSO authority per cell; we never stand a second IdP next to
a suite's own (the ADR's forbidden failure mode).

## Thin admin plane (Push B - outline only)

Three PII-free metrics (`sextant_build_info{version,fleet_model_version,
gate_mode}`, `sextant_upstream_last_check_timestamp`,
`sextant_rollout_active_rings`), a PodMonitor + NetworkPolicy keeping
them in-cluster, and one Grafana dashboard row per cell (name, host,
version, Ready, last reconcile, upstream check, active rings) over
Flux/kube-state-metrics. The boundary is ADR 0009's: the admin plane
manages the cells' existence, versions and health; it never reads
customer data, and there is no console superadmin.

## Upgrade and retire staging

Cells are rings, the same funnel Sextant applies to devices (ADR 0009
promise made concrete): `sextant-demo` (canary) -> `sextant` (own
dogfood) -> customer cells. An upgrade is a one-line `image.tag` +
`gateRunner.image.tag` commit per cell (the two move together);
rollback is `git revert`. Retirement is ordered - evidence export,
CNPG backup if the observed plane matters, revoke both deploy
credentials, archive (never delete: the overlay repo is the audit
trail) the repo, delete the OIDC client, then remove the two GitOps
paths so Flux prunes the namespace.

## Threat-model check (R1-R7)

- **R1** (owner-authored nix): unchanged in kind; per-cell gate-runners
  mean a customer's nix evaluates only inside their own cell. The
  template repo must stay free of shared secrets or shared cache keys,
  so no cross-cell material exists for it to exfiltrate.
- **R2** (shared check-in token): closed by construction for new cells -
  the token is omitted, per-device credentials carry check-in.
- **R3** (read-confidentiality): a cross-scope leak becomes a
  within-one-customer leak; the structural fix ADR 0009 promised.
- **R4** (force-push credential): narrowed - repository-limited token on
  a per-cell machine account, write to exactly one repo. Optional
  branch protection on `main` (no force-push) with `rings/*` left
  machine-owned is a runbook step.
- **R5/R6** (agent/update chain): unchanged by this design.
- **R7** (secret material at rest): per-cell secret store;
  `SEXTANT_SECRET_KEY` is in the runbook's key table before any
  imaging.
- **No new standing credential**: provisioning uses the operator's own
  forge login in the UI, minted artifacts are per-cell and
  least-privilege, so this design introduces no R8-class concentration
  (the reason the driver/CLI was cut).

## Non-goals

No operator/CRD, no cross-cell anything, no console superadmin, no
automation of secret material, no forge API automation (revisit
trigger above), no in-process multi-org routing (ADR 0009 keeps the
tenant field as defense in depth only).

## Open points

- Nicer core option for comin auth (`dawo.autoUpdate.options.
  tokenSecret`) - today the overlay re-declares the comin remote with
  auth (sextant-addon.nix); a fork-PR moves it into the core interface.
- Prod NetworkPolicy retrofit for `apps/sextant` after the demo canary
  proves the rule set.
- Forge driver + CLI: build when cell count or external operators make
  UI provisioning painful; the port sketch lives in this design's
  history (git log) if wanted.
