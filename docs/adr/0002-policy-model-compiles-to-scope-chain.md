# ADR 0002: Policies, assignments and filters compile to the scope chain

Status: accepted (2026-07-09)

## Context

The PoC stored settings inline on each scope (org, group, device) and
resolved them with a proven precedence rule: enforced values resolve most
general wins; default values resolve most specific wins. We want the richer,
Intune-style model - reusable named policies, bound to scopes by assignments,
narrowed by filters over device attributes - without rewriting the resolver
that already encodes the hard precedence math.

## Decision

Policies, assignments and filters are a pure compilation step. For a device,
the compiler gathers every assignment whose target lies on the device's scope
chain and whose filter matches the device, and emits an ordered chain of
virtual scope entries (alongside any inline scope settings). The existing
resolver then applies the unchanged precedence rule. Ties break on assignment
priority, then deterministic policy order. Every resolution carries
provenance: which policy or scope won and why.

## Consequences

- The resolver ports from the PoC nearly verbatim, tests included.
- New pure code is limited to the policy compiler and filter evaluator.
- The nix twin (resolve.nix) must implement the same compilation; a parity
  harness in CI proves equivalence.
- Inline scope settings remain supported, so simple fleets stay simple.
