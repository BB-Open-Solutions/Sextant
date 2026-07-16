# Manage secrets

Sextant handles two different kinds of secret, and the **Secrets** page (plus
a device's own page) is where both are managed. They are not interchangeable:
one is a *reference* an operator points settings at; the other is *material
the platform generated on a device's behalf* and holds so it can be recovered
later.

## Secret references (for settings and integrations)

A setting field that needs a secret value - a NetBird setup key, an LDAP bind
password, a Wazuh enrollment secret, an SMTP password - never accepts the
value itself in the console. Instead you register a **name**, and settings
pick that name from a list:

1. Open **Secrets**.
2. **Register a secret**: give it a name (`[a-z0-9][a-z0-9-]*`, e.g.
   `netbird-setupkey`) and an optional description.
3. Point any secret-typed setting field at it, in
   [Configuration editor](../concepts/safe-writes.md) or
   [Integrations](./integrations.md) - the field renders as a picker of
   registered names, with a shortcut to register a new one inline if you
   started from the setting itself.

The console never sees or stores the plaintext: only the *name* travels
through the config repo and Sextant's own state. The device resolves the
name to the decrypted material at runtime via agenix. Removing a registered
name breaks the build for anything still pointing at it - the console warns
before you confirm.

## Per-device secrets (break-glass recovery)

Some material is generated *for* a specific device during provisioning and
has nowhere else to live: a LUKS disk-encryption recovery passphrase, or a
break-glass local-administrator password. TPM2 enrolment makes the LUKS
passphrase unnecessary at day-to-day boot, but it remains the recovery path
if TPM2 unsealing ever fails - so it has to survive somewhere, encrypted at
rest, reachable only to someone who genuinely needs it.

Sextant seals this material (AES-256-GCM by default; a drop-in external key
manager such as OpenBao/Vault is the production posture) the moment it is
produced - at provisioning, from the imaging wizard - and never stores it in
the clear.

**Revealing** it:

- Reveal is **organisation-owner** reach only - not editor, not viewer.
- From a device's page (or the provisioning wizard, while a job is still
  fresh), *Reveal* shows the plaintext exactly once, rendered directly on the
  response - never redirected, so it never lands in a URL, browser history,
  or an access-log line.
- Every reveal is recorded: who, and when. There is no silent read.
- Once revealed, treat it as no longer fresh - the console flags a
  previously-revealed secret as such, since anyone who saw it once could
  have copied it.

If the secret store is not configured (no encryption key set), Sextant does
not store per-device secrets at all rather than write them in the clear - the
one-time value is then shown only at the moment it is generated (during
imaging) and never again.

## Troubleshooting

**A setting's secret picker is empty.**
No secret has been registered yet under that name pattern - register one on
the Secrets page first, or use the inline shortcut next to the field.

**Removing a secret reference breaks a build.**
Expected - any setting still pointing at that name fails the Nix gate on the
next change. Re-point the setting at a different registered name (or clear
it) before removing the reference, not after.

**"Reveal" is not available on a device.**
Either the secret store is not configured for this deployment (no
`SEXTANT_SECRET_KEY` / no external sealer wired up), no such secret was ever
generated for this device, or you are not signed in with organisation-owner
reach - reveal is deliberately not available to editors or viewers.

**The revealed LUKS passphrase does not unlock the device.**
Confirm you copied it in full (it is shown once, select-all) and that you
are unlocking the *current* value - if the device was re-imaged since the
secret was last generated, an old reveal (or a note copied from an earlier
session) no longer matches.
