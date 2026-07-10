# ADR 0010: Language per workload - Go server, Rust open for the agent

Status: accepted (2026-07-10)

## Context

Recurring doubt: is 100 percent Go right? The evidence after building the
system: the server is I/O-bound (every operation waits on git, nix,
Postgres or the network; one nix eval dwarfs all process CPU), runs in
128Mi, and leans on first-class Go libraries (net/http, go-oidc, go-ldap,
pgx). At the scale target the bottlenecks are Postgres writes and the nix
build farm, never the Go process. The PoC's Rust core was removed for
duplicating the resolver and drifting - the objection was drift, not Rust.

## Decision

- **The server stays Go.** Rewriting an I/O-bound service with a mature
  Go ecosystem fit buys nothing measurable and costs months.
- **The device agent (M3 redesign) will be Rust** (decided with Bram,
  2026-07-10). A small, long-running, resource-tight binary on every
  device is where Rust's footprint (no runtime, ~1MB) and strictness
  genuinely pay. It is a distinct component with a distinct job
  (check-in, converge, report) and shares no server logic, so the
  no-duplicate-implementations rule is not violated.
- The rule that stands: **never two implementations of the same logic.**
  The only sanctioned twin is nix/resolve.nix, guarded by the parity
  harness.

## Consequences

- The M3 agent is designed and built in Rust; its API contract is the
  existing check-in surface plus the lifecycle/remote-action intents.
- No language migrations elsewhere without a workload-based case.
