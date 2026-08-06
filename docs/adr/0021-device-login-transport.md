# 0021 - Device login uses LDAPS; plain LDAP is an acknowledged exception

## Status

Accepted 2026-08-06 (Bram). Reverses the route decision of 2026-07-27
("plain LDAP over the mesh is fine"), which was already recorded as
reversed on 2026-08-05 in `docs/1.0-fit-gap.md` **without a reason**. This
ADR supplies the reason, because a reversed security decision that carries
no argument is one somebody reverses back.

Narrows ADR 0015, which settles *which* authority issues tokens. This one is
only about the transport SSSD binds over. Nothing here changes the directory
as source of truth or the one-SSO-authority rule.

## Context

Device login is SSSD binding to the directory (`dawo.identity.*`). On a
**simple bind, the end user's password travels on that connection**. Not a
hash, not a ticket, not a scoped token: the password, in whatever the
transport gives it.

The 2026-07-27 decision said plain `ldap://` is acceptable because the
directory is only reachable through the WireGuard mesh, so the channel is
already encrypted and peer-authenticated at the network layer. That argument
is written into the overlay module's comments
(`modules/integrations.nix:325-357`) and into `docs/threat-model.md`.

**The argument does not survive contact with the path.** Three reasons, in
descending order of how much they matter:

1. **The mesh covers one leg, and the password crosses two.** WireGuard
   protects device to cluster. From the routing peer to the OpenLDAP pod the
   traffic is plaintext on the pod network. Anything that can capture there -
   a compromised sidecar, a node, a CNI-level debug tool, an operator with
   `kubectl debug` - reads staff passwords as they are typed. The
   confidentiality claim was being made about a hop the mesh does not carry.

2. **SSSD says so itself.** On the plain branch the module must set
   `ldap_auth_disable_tls_never_use_in_production = true`. That is upstream's
   name for the option, chosen by people who had this conversation before us.
   Measured 2026-08-06: it is set, in production, at bb-open
   (`identity.ldapUri = ldap://10.43.76.5`). We have been running the
   configuration whose own name is an instruction not to.

3. **Mesh membership is not the same as authorisation.** A peer that gets
   onto the mesh - a stolen NetBird setup key, a device not yet wiped - can
   reach the directory port and attempt binds. TLS does not prevent that
   either; the point is that "the mesh authenticates the peer" was being
   asked to do work it cannot do, and it made the plaintext look paid for.

A fourth reason is not a security argument but is real: BIO and ISO 27001
expect transport encryption for traffic carrying credentials, tunnel or no
tunnel. "We wrap it in WireGuard" is a position that has to be defended in
every audit, forever, by whoever is in the room. LDAPS is not.

**What is true in the original decision, and stays true.** It was not
careless. It came from a live failure on 2026-07-30 where SSSD sat offline
against a perfectly reachable directory: SSSD defaults
`ldap_id_use_start_tls` to true, so on a plain URI it asks for StartTLS, and
a directory that does not offer it answers "unsupported operation", which
SSSD counts as the server being down. The plain branch exists because
demanding TLS from a directory that has none does not produce a secure login,
it produces no login. That failure mode is unchanged and this ADR does not
pretend otherwise.

## Options

**A. LDAPS only; delete the plain branch.** Cleanest to argue. Breaks any
deployment whose directory has no certificate yet, with no way to proceed -
including a migration in progress, and a lab.

**B. LDAPS is the supported transport; plain LDAP requires an explicit,
recorded acknowledgement.** The deployment can still choose it, but not by
accident and not silently.

**C. Keep both as equals, document the risk.** What we have. It leaves the
default doing the unsafe thing and puts the burden on whoever reads the
comments.

## Decision

**Option B.** `ldaps://` is the supported transport for device login. A plain
`ldap://` URI is refused at evaluation unless the deployment sets an explicit
acknowledgement option, and a deployment that sets it gets a warning in the
build and a visible marker in the console.

This is the same shape as the chart's `gateMode: none` guard, which refuses
to render without an explicit ack (`deploy/helm`, ADR 0012). The house rule
is: an unsafe configuration stays reachable, because someone will genuinely
need it, but reaching it is an act, not a default. Two of these now agree,
which is worth more than either alone.

The acknowledgement is a fleet-document setting rather than a code edit, so
it lands in the audit log and the change-review flow like any other setting:
who turned off transport encryption for staff passwords, when, and against
which approval.

## Consequences

- **bb-open runs the unsafe branch today** and must move to `ldaps://`. The
  console's own directory bind is on the same footing
  (`ldap://openldap.ldap-bb-open:389` in the HelmRelease) and moves with it;
  it carries the `cn=sextant-ro` bind password rather than a user's, which is
  narrower but not different in kind.
- Zaanstad must be provisioned with LDAPS from the start. It is greenfield,
  so this costs a certificate, not a migration.
- The private-CA path already works: `identity.tlsCaCert` publishes the chain
  to `/etc/dawo/ldap-ca.pem` and the `ldaps://` branch verifies strictly
  (`ldap_tls_reqcert = "demand"`). Nothing new is needed for a directory
  signed by an internal CA.
- The 2026-07-30 offline failure is **not** re-opened by this: it belongs to
  the plain branch, which keeps `ldap_id_use_start_tls = false` for the
  deployments that acknowledge their way onto it.
- `docs/threat-model.md` loses the "deliberate choice" bullet and gains the
  residual risk this leaves: a deployment that acknowledges plain LDAP has
  staff passwords in the clear inside its own network, and the console can
  see that it does.

## Verification

Not closed when the module compiles. Closed when:

1. A fleet document with a plain `ldap://` URI and no acknowledgement is
   **refused by the gate**, with an error that names the option to set.
2. The same document with the acknowledgement evaluates, and the warning
   appears.
3. bb-open's own directory is reached over `ldaps://` and a staff login
   succeeds on hardware - the login path is the one that carries the
   password, so a bind test from the console is not the proof.
