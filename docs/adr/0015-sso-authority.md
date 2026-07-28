# 0015 - One SSO authority per deployment

## Status

Accepted 2026-07-28 (Bram): "LDAP is de source of truth als het op
users en hun wachtwoorden aankomt - zo doet Univention dat ook."
Originally drafted after the 2026-07-20 discussion. Settles which
identity component issues tokens when a deployment combines Sextant, a
directory and one or more IdP-capable products.

## Context

A DAWO deployment touches identity in three places:

1. **Console SSO**: Sextant authenticates operators via OIDC (today:
   Zitadel at bb-open; console RBAC derives roles from the IdP's group
   claims per request, ADR 0008).
2. **Device login**: municipal staff sign in on managed devices via SSSD
   against a directory (`dawo.identity.*`, vendor-neutral LDAP/AD/IPA).
3. **The customer's own stack**: a municipality may already run an
   identity suite. Univention Nubus notably bundles OpenLDAP AND a
   preconfigured Keycloak; other stacks pair a directory with ADFS,
   Entra or another IdP.

The failure mode this ADR forbids: two token-issuing IdPs side by side
over the same directory (e.g. Zitadel AND Keycloak both serving SSO for
different apps). That splits sessions, MFA policy, logout and audit into
two half-truths - the exact opposite of single sign-on.

## Decision

- **The directory is the source of truth for people and groups.** IdPs
  read from it; nothing else writes identity.
- **Exactly one SSO authority issues tokens per deployment.** Which one
  is a per-deployment (per-cell, ADR 0009) choice, not a product
  constant:
  - Bare directory (plain LDAP, no bundled IdP): **Zitadel** is the
    authority, with the directory as its user source. Device login goes
    straight to the directory via SSSD (device login is PAM/NSS, not
    OIDC - it does not multiply SSO authorities).
  - Suite with a bundled IdP (Nubus/Keycloak and kin): **the suite's
    IdP is the authority.** Sextant speaks standard OIDC and points at
    it directly. Deploying Zitadel next to it is forbidden unless it
    strictly BROKERS to the suite's IdP (no local accounts, no second
    login surface) - and brokering is the exception, justified per
    deployment, not the default.
- **Sextant stays IdP-neutral.** The console requires only standard
  OIDC + a group claim; the fleet side requires only `dawo.identity.*`.
  No Zitadel-specific behaviour may creep into either.

## Consequences

- Cell provisioning (ADR 0009) must treat the OIDC issuer, client and
  group-claim mapping as per-tenant configuration, with "customer
  brings their IdP" as a first-class path next to "we provision
  Zitadel".
- The integration test round (#72) gains a phase: console SSO against a
  Keycloak/Nubus issuer, proving the neutral-OIDC claim against a
  second implementation.
- Sales/tender answer becomes simple and sovereign: "we join your
  identity stack; we do not replace it."
- Device-login options stay directory-level (`dawo.identity.*`) and
  never grow an OIDC dependency; offline credential caching (task #5:
  sssd `cache_credentials` / `offline_credentials_expiration`) lands
  there regardless of which IdP fronts the web side.

## Rejected

- **SSSD coupled to the SSO directly (device login via OIDC)**
  (considered 2026-07-28): SSSD speaks no OIDC; the existing routes
  (PAM-OIDC modules, device-code flows at the greeter, Entra-specific
  agents) are immature and - decisive for this fleet - break offline
  login, where SSSD's cached LDAP credentials are proven. Linux login
  also needs posix attributes (uid/gid/home/shell), LDAP's native
  vocabulary. Device login stays SSSD -> directory; the SSO fronts the
  web only. Known weak spot of the accepted shape: password
  reset/self-service lives at the directory, not the IdP - acceptable
  at current scale (admin-assisted), Nubus is the step-up when that
  becomes the pain.
- **Zitadel always, everywhere**: forces a second IdP onto suite
  customers, splitting SSO - the forbidden failure mode.
- **Always broker through Zitadel**: keeps one console config at the
  cost of an extra moving part, an extra login redirect and an extra
  audit seam in every suite deployment; standard OIDC makes the broker
  unnecessary.
- **Per-app choice within one deployment**: two authorities, split
  sessions; forbidden.
