# ADR 0006: The interface grows by capability modules over metadata, Odoo-style

Status: accepted (2026-07-10)

## Context

The interface must absorb many more capabilities (lifecycle, updates,
remote actions, provisioning, compliance, multi-org) without turning into
the god-file console it replaces. Odoo demonstrates the sustainable
pattern: modules contribute models plus metadata, and generic views render
from that metadata; screens are rarely hand-built.

## Decision

Two levels, mirroring Odoo without its dynamic-plugin machinery (we are a
single Go binary; compile-time modularity is the honest equivalent):

1. **Settings screens are generated, not written.** The catalog
   (ADR 0005) is our `ir.model.fields`: one generic renderer maps
   catalog entries to widgets (bool -> toggle, enum -> select, string ->
   input, list -> tag editor), groups them by category, guards them by
   risk class and scope. New options mean zero UI code.

2. **Capabilities are compile-time modules behind a registry.** Each
   capability is one package exposing a small interface:

       type Capability interface {
           Name() string                      // nav entry + slug
           Routes(mux *ServeMux, deps Deps)   // API + pages
           Enabled(cfg Config) bool           // mounts only when configured
       }

   `cmd/sextant` iterates the registry the way Odoo installs modules:
   navigation, routes and permissions come from the module, not from a
   central god file. A capability that is not configured (no station
   token, no Postgres) simply does not mount - the UI shows what the
   deployment actually supports.

No runtime plugins, no dynamic loading: capabilities ship with the
binary, selected by configuration. That keeps the supply chain auditable
(one reviewed artifact) while preserving Odoo's growth model.

## Consequences

- Adding a capability = one package + one registry entry; the navigation
  and permission model extend themselves.
- Hand-written screens are the exception (dashboards, wizards); anything
  settings-shaped rides the generic renderer.
- Deployments differ by configuration, not by build; the audit story
  stays a single image digest.
