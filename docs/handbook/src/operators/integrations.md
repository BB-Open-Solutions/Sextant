# Integrations

Integrations are device-side capabilities the overlay publishes so you can turn
them on and configure them per scope from the console, without editing Nix. The
console shows a card per integration; a card is **available** once the overlay
publishes its options in the catalog, and **not published** otherwise.

Three ship with the BB Open overlay:

- **NetBird** - join a self-hosted WireGuard mesh, so a roaming device stays
  reachable and can pull and push from anywhere. You set the management URL and
  a setup key.
- **Directory login (LDAP)** - device login against your directory over SSSD
  (LDAP, AD or IPA). You set the provider, domain, server and a bind secret.
- **Wazuh** - an endpoint security agent that reports to a Wazuh manager. You
  set the manager address, an agent group and an enrollment secret.

## Secrets are references, never values

A field that holds a secret - a setup key, a bind password, an enrollment
password - is stored as a **reference**: the name of a secret you registered,
not the secret itself. The console renders it as a picker of registered names,
so a raw secret can never be typed into the console or committed to git. The
device resolves the name to the decrypted material at runtime (agenix); only the
name travels through the config repo.

Register the secrets first on the **Secrets** page (see
[Manage secrets](./secrets.md)), then point the integration field at one by
name.

## Setting it up

1. Open **Integrations**. A card that reads *available* is ready to configure.
2. Enable it and fill the fields. Secret fields offer the registered
   references, with a shortcut to register a new one inline.
3. Save. This writes at the **organisation** scope - the Integrations page is
   an org-wide quick-config surface for the catalog keys the overlay
   publishes. To narrow an integration to one group or device instead (or to
   review it alongside every other setting), open the same keys from
   **Settings** (the Configuration editor) and pick a group or device with
   its scope selector - integration settings flow down the scope chain like
   any other setting.
4. Every save passes the Nix gate and commits to git like every edit (or
   stages as a change, if your organisation requires change requests - see
   [Ship an update](./updates.md)).

If a card reads *not published*, the overlay has not exported that
integration's options yet: add its module to the overlay and regenerate the
catalog (`nix eval .#catalog --json > catalog.json`).

## Troubleshooting

**A card reads "not published" even though I added the overlay module.**
Regenerate the catalog (`nix eval .#catalog --json > catalog.json`) and
restart or reload the console's config snapshot - the catalog is generated,
not live-read from the overlay's Nix source.

**Saving an integration field fails the gate.**
Same as any setting - the distilled error names the actionable line (e.g. an
out-of-range value or an unknown option). See
[Safe writes and the Nix gate](../concepts/safe-writes.md).

**A secret field's picker is empty.**
No secret reference has been registered yet - use the inline shortcut next
to the field, or register one first on the [Secrets](./secrets.md) page.
