# ADR 0008: API and credential security model

Status: accepted (2026-07-10)

## Context

The API is the product: everything the interface does, a token holder can
do headlessly. Today (Tier 0) two static bearer tokens exist: one API
token that acts as owner-everywhere, and one shared device check-in
token. Acceptable for a PoC, not for audited organisations: the API token
is an unscoped master key, and a shared device credential lets any device
impersonate any other. This ADR fixes the target model and the path.

## Decision

### Principals and credentials

1. **Humans**: OIDC sessions only (built). Roles derive per request from
   the access configuration; nothing is trusted from the cookie.
2. **Personal API tokens, Odoo-style.** A user creates tokens for
   themselves; a token acts AS that user. Its rights are derived per
   request from the user's current bindings through the same identity
   resolver as the session - the token can never exceed its owner, and
   revoking the user's access instantly limits every token they hold.
   A token may carry an optional ceiling that narrows it further (e.g.
   viewer-only for a dashboard integration), never widens it. Group
   membership rides a snapshot with a bounded TTL: expiry forces
   re-issuance, so a user removed at the IdP loses token power within
   the TTL at the latest, immediately when their bindings are revoked
   in the fleet document.
3. **Service accounts** for non-human automation (CI, integrations):
   named principals with explicit bindings in the access list, same
   resolver, no user attached. Both kinds are stored hashed (argon2id),
   show last-used, and are revocable and rotatable from the console.
   The current env token remains as break-glass, explicitly labeled,
   until this lands.
4. **Devices**: per-device credentials issued at enrollment, bound to the
   tag; a check-in authenticates the device it claims to be. Rotation on
   re-image; revocation on retire (part of the lifecycle capability).
   The shared check-in token remains only as a migration bridge.

### Guarantees (mostly built, now contractual)

- Secrets enter via environment/files only, never argv; cookies are
  AES-256-GCM; comparisons constant-time; login and check-in rate-limited.
- Input hits a closed vocabulary: slug validation, setting-key whitelist,
  filter grammar, and the nix eval gate as final firewall. Data can never
  carry code.
- Every API mutation is attributed (principal -> git author) and lands as
  an auditable commit; API calls on sensitive routes are logged with
  principal and outcome.
- Supply chain: vendored dependencies, reproducible builds, one reviewed
  image digest, CI-gated merges, no runtime plugins (ADR 0006).

### Explicitly rejected

- Long-lived unscoped tokens as a supported mode (break-glass only).
- Remote code execution channels of any kind, including "run script on
  device" (see ADR on remote actions in capabilities.md).
- Storing plaintext credentials server-side.

## Consequences

- New capability work: service accounts (domain + store + console page)
  and per-device credentials in the enrollment flow.
- A security review gate joins the definition of done for
  credential-touching changes; threat model lives with this ADR and is
  updated per capability.
- Known gap until then, stated honestly: the Tier-0 tokens. Deployments
  must treat them as root credentials.
