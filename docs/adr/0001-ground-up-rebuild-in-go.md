# ADR 0001: Ground-up rebuild in Go, hexagonal architecture

Status: accepted (2026-07-09)

## Context

The Sextant proof of concept (dawo-fleet-console) proved the product: a
config-as-data control-plane with a safe git write path gated by nix eval.
Its domain logic was sound and well tested, but the packaging was not
production-grade: a 3400-line web god-file mixing transport, business logic,
git/exec and templating; an unserialized git write path; critical state held
in memory; no CI, graceful shutdown, metrics or rate limiting; and a dead
Rust core crate duplicating the resolver.

## Decision

Rebuild in a fresh repository with a hexagonal architecture (pure domain,
use-case services, ports, adapters, thin transport), in Go, as a single
static binary. Delete the Rust core. Port the proven pure-logic cores from
the PoC together with their tests (scope resolver, safe-write transaction,
nix gate, injection whitelist) instead of re-deriving them.

Go over Rust: Go is memory-safe, and this is an I/O-bound orchestration
service on Go's home turf (net/http, go-oidc, go-ldap, pgx). The safety
property of this system is the nix eval gate plus the injection whitelist
plus tests, not the implementation language. One language means one resolver
and no parity twin to drift.

## Consequences

- Every capability lands behind a port with tests before it ships.
- The PoC repo remains the reference for behaviour until cutover.
- A Go-to-nix parity harness (not a second implementation) guards the
  resolver contract with resolve.nix.
