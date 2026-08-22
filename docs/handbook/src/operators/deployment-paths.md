# Three ways to run Sextant

One binary, three deployments. They differ in what carries the database, the
secrets and the TLS, not in what the console does.

Every command here was run on 2026-08-22 against release 0.91.0, and the traps
are the ones that actually bit.

| | What it is | What it is for |
|---|---|---|
| `just demo` | console, throwaway database, simulated fleet | seeing it work, in a minute, on your own machine |
| A container | one image, your database, your proxy | a single host, NixOS or anything else that runs podman |
| The Helm chart | console, gate-runner, CloudNativePG | a cluster, and the only path with HA and backups |

There is also a **NixOS module** (`nixosModules.default` in the flake) which
runs the same binary under systemd with `DynamicUser`. It is the container path
without the container; the same database and secret rules apply.

## 1. `just demo`

```sh
git clone https://codeberg.org/DAWO/DAWO-Sextant.git && cd DAWO-Sextant
just demo
```

Console on **http://127.0.0.1:8080** with sixty simulated devices, a wave plan
and a working imaging line. Ctrl-c stops everything and deletes the directory,
including the database.

Needs `initdb`, `pg_ctl` and `createdb` on PATH; `nix develop` provides them.

It runs `--dev-auth --gate none --allow-unvalidated`, which is why it is a demo:
a synthetic owner session on loopback and no Nix validation. **The gate cannot
be exercised locally at all** - the example overlay takes Sextant as a path
input, which stops resolving once the overlay is a git repository (issue #74).

## 2. A container, on NixOS or anywhere

```sh
podman run -d --name sextant --network host \
  -e SEXTANT_PG_DSN="postgres://sextant@127.0.0.1:5432/sextant?sslmode=disable" \
  -e SEXTANT_CHECKIN_TOKEN="…" \
  -e SEXTANT_SECRET_KEY="$(head -c 32 /dev/urandom | base64)" \
  -v /srv/sextant/overlay:/data/overlay:z \
  forgejo.bb-open.com/bb-open/sextant:0.91.0 \
  --addr 127.0.0.1:8080 --repo /data/overlay --write --gate remote \
  --gate-url https://gate.example.org
```

**The image runs as uid 65532 and will not start on a volume it cannot write.**
The state directory defaults to `<repo>/.sextant-state`, so a bind mount owned
by your own user fails with:

```
sextant: state dir: mkdir /data/overlay/.sextant-state: permission denied
```

Chown the volume to that uid before the first run (`podman unshare chown -R
65532:65532 /srv/sextant/overlay` for a rootless podman), or point `--state-dir`
at a volume that is writable.

**`--dev-auth` only works on loopback**, which inside a container is the
container's own. `--network host` is why the example above works; a real
deployment uses an IdP and does not need it.

Verified on 0.91.0: five capabilities mounted, `/station` answered 200, and a
setting saved in the console landed in `fleet.json` inside the mounted overlay.

Put TLS in front of it. Behind TLS on a non-loopback address the console
refuses to ship session cookies without `--secure-cookies`, deliberately.

## 3. The Helm chart

```sh
kubectl create namespace sextant
kubectl -n sextant create secret generic sextant \
  --from-literal=SEXTANT_CHECKIN_TOKEN='…' \
  --from-literal=SEXTANT_SECRET_KEY="$(head -c 32 /dev/urandom | base64)" \
  --from-literal=SEXTANT_SESSION_KEY="$(head -c 32 /dev/urandom | base64)" \
  --from-literal=SEXTANT_OIDC_CLIENT_SECRET='…'
helm install sextant ./deploy/helm -n sextant -f my-values.yaml
```

This is the only path that brings its own database: the chart creates a
CloudNativePG cluster and wires the console to it, so `SEXTANT_PG_DSN` is not
in that secret. It needs the CloudNativePG operator installed first.

The full walk-through, including the values that bite, is
[Install and configure Sextant](./deploy.md).

## What differs, and what does not

The console is the same binary in all three. What changes:

- **The database.** The demo makes one and throws it away; the container and
  the NixOS module expect one; the chart creates one.
- **Validation.** The demo has no gate. The container and the chart should use
  `--gate remote` with a nix-capable gate-runner - the console image ships no
  nix on purpose.
- **Backups.** Only the chart has an opinion, and its backup is **off** by
  default while the database holds the only copy of the LUKS recovery keys.
  Whichever path you take, that is your decision to make and not one to
  inherit.
