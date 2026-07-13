# Install and configure Sextant

Git is not an integration in Sextant - it is the storage. A fleet's whole
configuration is **data in a git overlay repository** (`fleet.json`, policies,
overlays). Sextant reads and writes that repository directly: every change is
an audited commit pushed to the remote, and the remote is the source of truth.
So "connecting Sextant to git" means pointing the console at your overlay repo;
there is nothing else to wire.

## What you need

- **An overlay repository** - a git repo that consumes the DAWO core flake and
  holds your `fleet.json`. One repo per organisation (tenant). It is the same
  repo the devices follow via comin.
- **Postgres** - the observed plane (check-ins, tokens, image jobs, prefs). A
  single instance next to the console is fine; it partitions per tenant.
- **An OIDC identity provider** - console login (one IdP per instance), mapped
  to roles by group. Optional LDAP for the group-picker source.
- **A validation gate** - `eval` runs nix in-process; `remote` delegates to a
  small nix-capable gate-runner (the console image itself ships no nix). Use
  `remote` in production; it is fail-closed.

## Connect the console to your overlay repo

Point the console at the repo and a push remote:

```
--repo /data/overlay        # the working tree the console edits
--git-remote origin         # the push remote (HA source of truth)
```

Under Helm (`deploy/helm`), the same as values:

```yaml
gitRemote:
  url: https://your-forge.example.com/org/sextant-overlay.git
  branch: main
  netrcSecret: sextant-overlay-netrc   # credentials for a private remote
gateMode: remote                       # or eval / none
oidc:
  issuer: https://id.example.com
  clientId: "<client-id>"
```

The console clones the repo, keeps its snapshot in sync (the remote is
authoritative - commits made by engineers or CI show up without a restart),
and the devices follow the same repo: comin on each device tracks
`rings/<group>` (or `main`), so a rollout that advances a ring branch lands on
the devices in that ring.

## First deploy, end to end

1. Create the overlay repo from the DAWO core (a `fleet.json` with your org and
   the core as a flake input). Push it to your forge.
2. Deploy the console (Helm chart, container, or the NixOS module) with the
   `gitRemote`, `gateMode`, `oidc` and Postgres settings above. Behind TLS,
   set `--secure-cookies` (the console refuses session cookies without it on a
   non-loopback address).
3. Deploy a gate-runner if `gateMode: remote`; it keeps a warm clone of the
   overlay and evaluates each candidate before the console commits.
4. Log in via your IdP. Enroll a device, assign it to a group, edit settings -
   every change passes the gate and commits to the overlay. Stage a rollout to
   land updates in waves.

## Notification e-mail (SMTP)

In-app notifications work with no extra setup. To also deliver them by mail,
an owner configures SMTP per organisation under **E-mail (SMTP)** in the
console (host, port, from, security). The password is set one of two ways:

- **A secret reference** (recommended) - enter the *name* of a secret; the
  value lives in agenix or a cluster Secret mounted at `SECRET_DIR` (default
  `/run/secrets/<name>`). Sextant reads only the name, never storing the value.
- **A typed password** - available only when `SEXTANT_SECRET_KEY` is set (a
  base64 32-byte key). The password is then sealed (AES-256-GCM) and stored in
  Postgres. Without the key this option is disabled and the console says so.

Both can also be set at deploy time. `SEXTANT_SECRET_KEY` is an environment-only
secret; add it to the same secret the chart mounts (`secretName`).

Multi-tenant (model B): one overlay repo per organisation, isolated stores,
one console instance per repo. See `docs/adr/` for the decisions behind this.
