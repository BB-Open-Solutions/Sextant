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

## Before you start

| | |
|---|---|
| Go | 1.25 or newer, to build from source |
| PostgreSQL | The chart brings its own via CloudNativePG. For `just demo` you need the client tools (`initdb`, `pg_ctl`, `createdb`) on your PATH |
| Kubernetes | any recent version; the chart uses no alpha APIs. Helm 3 |
| CloudNativePG | the operator must be installed before the chart, which creates a `postgresql.cnpg.io/v1` Cluster. Skip it with `cnpg.enabled: false` and bring your own database |
| Nix | only on the gate-runner. The console image ships none, deliberately |

To see it working before you deploy anything, `just demo` starts a console, a
throwaway database, sixty simulated devices and an imaging line on your own
machine, and deletes all of it on ctrl-c.

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

## Settings that are environment-only

Secrets never go in the fleet document or in Helm values as plain text. These
are read from the environment, and the chart mounts them from one secret
(`secretName`):

| Variable | What it is |
|---|---|
| `SEXTANT_PG_DSN` | The observed plane's database. The chart sets it for you from the CloudNativePG cluster it creates (`cnpg.enabled: true`); supply it yourself only when you point at your own Postgres with `cnpg.enabled: false`. **Without it the console starts anyway** and mounts three capabilities instead of five: no device status, no compliance verdicts, and `/station` answers 503. That is a working config plane and half a product, so check it on a first deploy rather than wondering later. |
| `SEXTANT_CHECKIN_TOKEN` | The shared token devices present when they check in. The agent carries the same value. |
| `SEXTANT_SECRET_KEY` | Base64 of exactly 32 bytes. Seals typed secrets (SMTP passwords, LUKS recovery keys) at rest. Without it those features disable themselves and say so, rather than storing anything in the clear. |
| `SEXTANT_SESSION_KEY` | Base64 of exactly 32 bytes. Seals session cookies. **Required** as soon as an OIDC issuer is set: the console refuses to start without it rather than fall back to something weaker. Keep the same value across replicas and restarts, or everyone is logged out. |
| `SEXTANT_API_TOKEN` | Bearer token for the machine API. |
| `SEXTANT_OIDC_CLIENT_SECRET` | The console's OIDC client secret. |
| `SEXTANT_LDAP_BIND_PASSWORD` | Bind password, when LDAP supplies the group picker. |

Every flag has an environment equivalent under the same prefix
(`SEXTANT_SECURE_COOKIES`, `SEXTANT_TRUST_PROXY`, `SEXTANT_SHUTDOWN_GRACE`,
and so on); `sextant --help` lists the flags.

## First deploy, end to end

1. Create the overlay repo from your NixOS core (a `fleet.json` with your org and
   the core as a flake input). Push it to your forge.
2. Deploy the console from the chart in this repository:

   ```sh
   kubectl create namespace sextant
   kubectl -n sextant create secret generic sextant \
     --from-literal=SEXTANT_CHECKIN_TOKEN='...' \
     --from-literal=SEXTANT_SECRET_KEY="$(head -c 32 /dev/urandom | base64)" \
     --from-literal=SEXTANT_SESSION_KEY="$(head -c 32 /dev/urandom | base64)" \
     --from-literal=SEXTANT_OIDC_CLIENT_SECRET='...'
   helm install sextant ./deploy/helm -n sextant -f my-values.yaml
   ```

   The chart creates a CloudNativePG cluster and wires the console to it, so
   the DSN is not in that secret. Its backup is **off** by default, because a
   default pointing at an object store this chart cannot assume would fail
   every install - and this database holds the only copy of the LUKS recovery
   keys, since a device erases its own once the console acknowledges it. Turn
   `cnpg.backup` on before the fleet grows.

   Or the container, or the NixOS module, with the
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

**Devices check in but the console shows no status, and /station is 503.**
The observed plane has no database. The console mounts what it can and keeps
going rather than refusing to start, so this looks like a product missing
features rather than a missing setting. Check `SEXTANT_PG_DSN` reached the
pod, and that the CloudNativePG cluster is `Ready` if the chart created one.

**SMTP is configured but no mail arrives.**
Confirm the password resolved: a secret reference must exist under
**Secrets** with a value the runtime can actually read (agenix or the
mounted `SECRET_DIR`); a typed password needs `SEXTANT_SECRET_KEY` set on
the deployment, or the console disables that option outright.
