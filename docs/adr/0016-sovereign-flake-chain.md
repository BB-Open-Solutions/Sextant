# 0016 - Sovereign flake chain

## Status

Proposed. Decision draft for review (Bram); captures the 2026-07-20
discussion on full independence from external flake sources. Phase 1 is
partially underway (the Forgejo fork of the core exists).

## Context

A DAWO fleet today evaluates against sources we do not control:

- `nixpkgs` and every transitive flake input come from github.com,
  pinned by `flake.lock` but fetched remotely on a cold store.
- Binary substitution comes from `cache.nixos.org`.
- The DAWO core itself lives at code.overheid.nl under MinBZK - which
  we treat as upstream and must not write to (fork-only policy).

For a product sold to municipalities on sovereignty, that is three
external availability-and-integrity dependencies in the build path.
An outage makes fleets unable to build; a compromise upstream is a
supply-chain vector. The counterweight already exists in the
architecture: the upstream watcher (ADR/0.65.x) is a single, audited
intake point where new core revisions enter as staged change requests.

## Decision

Make the intake point the ONLY place the outside world touches the
chain, in three phases:

1. **Source mirrors.** Forgejo (bb-open, later the platform org)
   carries pull-mirrors of the DAWO core and every external flake
   input, nixpkgs included. Overlays and devices reference only mirror
   URLs - either rewritten inputs or an internal flake registry that
   pins every name to its mirror. The core fork
   (forgejo.bb-open.com/bb-open/dawo-nixos) is the first mirror; the
   MinBZK repo stays upstream and read-only for us.
2. **Own binary cache.** The gate-runner's release cache (now on a
   PVC, chart 0.65.11) grows into a fleet-wide substituter signed with
   our own key: gate/CI builds populate it, devices list ONLY this
   cache and trust ONLY this key. cache.nixos.org disappears from
   device configuration; the builder may still substitute from it
   during intake, which keeps the trust decision at the intake point.
3. **Source archive (air-gap tier, optional).** A pull-through store
   for the fetchurl/fetchgit tarballs nixpkgs itself downloads, so a
   from-source rebuild also needs nothing external. Only justified by
   a real air-gap requirement; phases 1+2 already remove the external
   path from normal operation.

The upstream watcher orchestrates: new upstream revision -> mirror
sync -> internal build into the cache -> staged core CR through the
normal review/rollout chain. Compromise or outage upstream degrades to
"no new updates today", never to "fleet cannot build".

## Consequences

- Platform work first (bb-open-platform-v2): mirror org + sync jobs,
  cache service with its signing key (the release-cache key mechanism
  exists), registry config in the overlay recipe.
- The overlay's `dawo` input repoints from code.overheid.nl to the
  mirror - a one-line change per overlay, but a trust-model change
  that belongs to this ADR's acceptance, not to a drive-by commit.
- Tender answer: the entire software supply chain resolves inside the
  customer's (or our) infrastructure, with one audited intake.
- Cost: mirror storage is small; the cache is the release cache we
  already run. The real cost is operating the sync jobs and owning
  key rotation.

## Rejected

- **Vendoring sources into each overlay repo**: couples every tenant
  to gigabytes of vendored history and makes updates a repo-surgery
  exercise; mirrors centralise the same guarantee.
- **Trusting cache.nixos.org on devices with our key as a second
  trust root**: two roots = the weaker one wins; devices trust only
  ours.
- **Air-gap tier by default**: real cost for a guarantee phases 1+2
  already deliver operationally; keep it demand-driven.
