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

Register the secrets first on the **Secrets** page, then point the integration
field at one by name.

## Setting it up

1. Open **Integrations**. A card that reads *available* is ready to configure.
2. Enable it and fill the fields. Secret fields offer the registered references.
3. Set it at the organisation to cover the whole fleet, or at a group or device
   to narrow it - integration settings flow down the scope chain like any other
   setting.
4. Save. The change passes the Nix gate and commits to git like every edit.

If a card reads *not published*, the overlay has not exported that
integration's options yet: add its module to the overlay and regenerate the
catalog (`nix eval .#catalog --json > catalog.json`).
