# Sextant

Declarative fleet control-plane for NixOS. Config-as-data in git, built by nix,
deployed by comin. Sextant is the human and API surface that edits fleet
configuration safely, proves it builds, stages the rollout, and reports what
each device actually runs. Devices PULL their configuration; the console never
pushes to them. The few things that must reach a machine directly - lock a
session, collect diagnostics, cryptographically erase a lost laptop - travel
as intents the device picks up and a root executor carries out, never as a
remote command channel.

## Status: Beta

The rebuild is feature-complete for its first production use and is being
prepared for one. Running it: the fleet this is developed against runs it,
including imaging, rollouts in rings, directory login, endpoint security and
disk-encryption escrow on real hardware.

Beta means the shape is settled and the remaining work is proving it rather
than designing it. Expect the APIs and the fleet document schema to stay put;
expect rough edges in places the first fleet has not exercised yet, and expect
us to say which those are rather than pretend otherwise.

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
