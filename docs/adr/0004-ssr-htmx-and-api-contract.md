# ADR 0004: Server-rendered UI with htmx; /api/v1 is the machine contract

Status: accepted (2026-07-09)

## Context

Sextant is an operations console. The PoC rendered server-side with Go
html/template and no JS framework, which kept deployment to one binary but
hardcoded Dutch labels in Go and offered limited interactivity. A separate
SPA would raise the UX ceiling at the cost of a second codebase, a JS build
toolchain, and a larger surface for quality drift.

## Decision

Keep the UI server-rendered with html/template, add htmx (vendored, embedded)
for partial updates and interactivity. All labels come from a message catalog
(English default); no user-facing strings in Go code. The JSON API under
/api/v1 is the stable machine contract: dfctl, CI, AI agents and any future
SPA are all clients of the same API the UI capabilities map onto.

## Consequences

- One binary, no JS build step; assets embed via go:embed.
- The API is designed and versioned deliberately from phase 2 on; UI-only
  behaviour that bypasses the app services is not allowed.
- If a richer frontend is ever needed, /api/v1 already supports it.
