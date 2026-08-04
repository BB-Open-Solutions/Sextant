# Install and configure Sextant

Git is not an integration in Sextant - it is the storage. A fleet's whole
configuration is **data in a git overlay repository** (`fleet.json`, policies,
overlays). Sextant reads and writes that repository directly: every change is
an audited commit pushed to the remote, and the remote is the source of truth.
So "connecting Sextant to git" means pointing the console at your overlay repo;
there is nothing else to wire.

## What you need

- **An overlay repository** - a git repo that consumes a NixOS core flake and
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

1. Create the overlay repo from your NixOS core (a `fleet.json` with your org and
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

## Build-before-promote

At scale, ring promotion should not mean 10,000 devices each independently
compiling the same closures on weak edge hardware. With `gateMode: remote`
and a gate-runner `cache` configured (a signing key secret, and optionally a
dedicated cache host), a rollout's wave builds its release into that signed
binary cache *before* its ring branch flips - devices then substitute
(download) the pre-built closure instead of building it. Enable the console
side with `releaseCache: true`. See
[Scaling to 10,000+ devices](../architecture/scale.md) for the reasoning, and
[Ship an update](./updates.md) for what a wave's **Building** status means
day to day.

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

## Troubleshooting

**The console refuses to start / refuses session cookies.**
Behind TLS on a non-loopback `--addr`, Sextant refuses to ship session
cookies without `--secure-cookies` (or `SEXTANT_SECURE_COOKIES=true`) - this
is deliberate fail-closed behaviour, not a bug. Set the flag.

**Every write is refused with `gateMode: remote`.**
The gate is fail-closed: no reachable gate-runner means no writes, by
design. Check the gate-runner's `/healthz` before flipping `gateMode` to
`remote`, and after any gate-runner redeploy.

**A rollout wave never leaves "Building".**
With build-before-promote enabled (`releaseCache: true` +
`gateRunner.cache`), check the gate-runner's cache is healthy and its signing
key secret is present - a wave cannot promote until its release lands in the
signed cache.

**SMTP is configured but no mail arrives.**
Confirm the password resolved: a secret reference must exist under
**Secrets** with a value the runtime can actually read (agenix or the
mounted `SECRET_DIR`); a typed password needs `SEXTANT_SECRET_KEY` set on
the deployment, or the console disables that option outright.
