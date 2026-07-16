# Enterprise audit — July 2026

Scope: the full DAWO-Sextant repository (console, gate-runner, Rust device
agent, CLIs, packaging, CI) audited against current production-engineering
practice (Google/Uber Go guides, Rust API guidelines, OWASP ASVS 5.0, SLSA,
CIS container hardening, Helm chart conventions). Method: six parallel
specialised reviews plus an independent best-practice research pass; every
tool-verifiable claim was executed, not inferred. All fixes landed the same
day in four batches, each merged green through the full race suite, lint and
the Forgejo workflow.

## Verdict

The core is sound. Layering is clean (zero I/O imports in the domain, zero
exec in transport, no adapter-to-adapter coupling), the security surfaces
verified tight (parameterised SQL throughout, injection firewalls on every
git/nix argv boundary, central CSRF, guarded redirects, hardened session
cookies, digest-pinned non-root images), the test culture is real (race-clean
31/31 packages, hermetic Postgres, RSA-verified fake IdP — no mock theater),
and the device agent is exemplary (zero unsafe, zero runtime unwraps, zero
clippy warnings at -D warnings, credentials never logged).

The debt found was at the edges, and is now fixed.

## Fixed in this audit

| Area | Finding | Fix |
|---|---|---|
| Authz (HIGH) | Personal-token group snapshots lived out a 90-day TTL after group removal | Snapshots pruned against the live cached directory on every authentication; default TTL 30 days |
| Tests (HIGH) | gate-runner release build path (the cache devices trust) 0% covered | Seams for git/publish effects; full handler suite (auth, injection edges, idempotent job, failure detail, cache serving) |
| Tests (HIGH) | SMTP Send/dial 0% covered | In-process fake SMTP server; 21% → 88%, proving TLS verification is genuinely enforced |
| Packaging (HIGH) | No release workflow — images pushed by hand | Tag-triggered release workflow (all images + chart-version guard); requires the REGISTRY_TOKEN secret once |
| CI (MED) | catalog.json drift unguarded; chart never linted | Drift guard + helm lint/render steps in CI; regen script made colour-safe |
| CI (MED) | No recurring vulnerability gates | Blocking govulncheck + cargo-audit steps (0 findings at introduction) |
| Correctness (MED) | auxOnce rollback failure silent | Loud (errors.Join), mirroring applyTx; aux gate calls adopt shape-class sampling |
| Cohesion (HIGH/MED) | pages.go (740), config.go (597), web.go (514) mixed unrelated axes | Split into cohesive per-area files; nothing outside i18n data now exceeds 400 lines |
| CLI (BUG) | `sxctl evidence FROM TO` silently ignored the date range | Fixed; regression-tested. Found BY the new test sweep (0% → 77% coverage) |
| Hygiene | 71 orphaned i18n keys, 4 dead exports, swallowed test-setup errors | Removed / made loud |
| Agent | No backoff on failed check-ins (10k thundering herd) | Exponential backoff ×2..×16 with growing jitter; credential redacted from Debug |
| Dev drift | `just ci` narrower than real CI; dockerignore shipped 290 MB Rust cache | Mirrored exactly; excluded |

## Residual risks (documented, accepted for now)

1. **Membership-removal staleness**: a user removed from a still-existing
   group keeps token rights up to 30 days. Closing this needs a per-user
   membership adapter (Zitadel API); tracked for the cells workstream.
2. **PR-time image builds**: CI lints the chart and builds the nix packages,
   but container builds are proven at release time only, until the runner's
   podman capability is confirmed by the first tagged release.
3. **Legacy repo**: the pre-rebuild `dawo-fleet-console` carries a token-on-
   argv flaw in its dfctl; recommendation is archiving the repository, not
   patching it.
4. **SBOM/provenance**: not yet attached to releases (SLSA ambition);
   scheduled with the release-workflow follow-up.

## Reference

Full per-area findings live in the session's audit working set; measured
scale evidence in [architecture/scale](../handbook/src/architecture/scale.md).
