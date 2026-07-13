# Sextant

Declarative fleet control-plane for NixOS. Config-as-data in git, built by nix,
deployed by comin. Sextant is the human and API surface that edits fleet
configuration safely, proves it builds, stages the rollout, and reports what
each device actually runs. Not MDM: declarative pull, no remote wipe.

## Status: Alpha - not for production use

This is an **alpha** release: a ground-up rebuild of the Sextant proof of
concept with a production architecture, published early and openly. It is
**not yet intended for production use** - APIs, schemas and behaviour may still
change.

**Help wanted:** developers, testers and maintainers. If you would like to
collaborate, please contact Bram Buijs at **b.buijs@bb-open.com**.

See `docs/adr/` for the decisions and `docs/architecture.md` for the design.
Contributors welcome - see CONTRIBUTING.md.

## Architecture (short)

Hexagonal: pure domain, use-case services, ports, adapters, thin transport.

```
internal/domain    pure model + scope/policy resolution (no I/O)
internal/app       use-case services
internal/ports     interfaces the app depends on
internal/adapters  git, nix, postgres, ldap, oidc, integrations
internal/http      SSR web (html/template, form-POST) and /api/v1 JSON
internal/platform  config, logging, metrics, server lifecycle
```

## Build and test

```
just ci      # fmt-check, vet, lint, test -race, build
just run     # run the server locally
```

Or without just: `go test -race ./... && go build ./...`.

## License

See LICENSE.
