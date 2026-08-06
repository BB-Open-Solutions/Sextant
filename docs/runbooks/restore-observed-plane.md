# Restoring the observed plane

What this database holds, and what it costs to lose it, decides how much of
this runbook you need:

| Loss | Rebuildable from | Cost |
|---|---|---|
| Device inventory, check-ins | the fleet document plus the devices themselves | a check-in interval |
| Image jobs, discovery | the station, on the next report | minutes |
| Notifications, prefs, audit tail | nothing, but nothing depends on them | annoying |
| **LUKS recovery-key escrow** | **nothing** | **every encrypted device in the fleet becomes unrecoverable** |

That last row is why this exists. A device erases its own copy of the
recovery key as soon as the console acknowledges it (design 0009), so this
database holds the only copy of the material that gets a user back into an
encrypted disk. It is the one thing here that no amount of waiting rebuilds.

The config plane is not in scope: that is git, it has two remotes and a
clone on every device.

## Before you need it

Two things have to be true, and both are chart values:

1. `cnpg.backup.enabled=true` with `destinationPath`, `endpointURL` and
   `secretName` set. The secret must exist in the console's namespace and
   hold `ACCESS_KEY_ID` and `SECRET_ACCESS_KEY`.
2. A base backup exists. The chart renders the ScheduledBackup together with
   the object store for exactly this reason, and CI asserts the pairing -
   but verify it once with your own eyes:

```
kubectl -n <ns> get cluster <name> \
  -o jsonpath='{.status.firstRecoverabilityPoint}{"\n"}'
```

**An empty answer means you have no backup**, whatever the bucket looks
like. WAL segments with nothing to replay them onto are not a backup. That
state sat unnoticed on the forgejo cluster in this same platform until
2026-06-01.

## Restoring into a new cluster

CNPG restores by bootstrapping a *new* Cluster from the object store; you do
not restore into the existing one. Set `cnpg.enabled=false` in the
HelmRelease so the chart stops managing the old Cluster, then apply a
recovery Cluster by hand:

```yaml
apiVersion: postgresql.cnpg.io/v1
kind: Cluster
metadata:
  name: sextant-pg-restored
  namespace: sextant
spec:
  instances: 1
  storage:
    size: 2Gi
    storageClass: longhorn
  bootstrap:
    recovery:
      source: sextant-pg-backup
      # Omit recoveryTarget for the latest available point. To restore to a
      # moment before a bad write, set it here - that is the whole reason WAL
      # archiving exists next to the base backup.
  externalClusters:
    - name: sextant-pg-backup
      barmanObjectStore:
        destinationPath: s3://bbopen-backups/sextant/v1/
        endpointURL: https://nbg1.your-objectstorage.com
        s3Credentials:
          accessKeyId:
            name: hetzner-bbopen-backups-nbg1
            key: ACCESS_KEY_ID
          secretAccessKey:
            name: hetzner-bbopen-backups-nbg1
            key: SECRET_ACCESS_KEY
        wal:
          compression: gzip
```

Then point the console at it: the DSN comes from the CNPG-generated secret
`<cluster>-app`, so `cnpg.name` in the HelmRelease has to name the restored
cluster (or copy the secret across).

## Verifying a restore, properly

Do not verify by whether the console starts. It starts fine against an empty
database, which is the failure mode this whole runbook is about.

```sql
-- Escrowed recovery keys, the row that matters.
SELECT count(*), max(created) FROM device_secrets;
-- Devices known to the observed plane.
SELECT count(*), max(last_seen) FROM device_status;
```

Compare both against what the console showed before the loss, and then
**reveal one recovery key through the console and check it against the
device**. Sealed rows that decrypt to nothing look exactly like sealed rows
that decrypt correctly until somebody tries: the sealing key lives in
`SEXTANT_SECRET_KEY`, not in the backup, so a restore into a deployment with
a different key gives you rows you cannot open.

That is the trap worth stating plainly: **the backup is useless without the
sealing key**, and the sealing key is not in it. Whatever holds
`SEXTANT_SECRET_KEY` needs its own escrow, or this runbook restores
ciphertext nobody can read.

## Status

- Chart support: **done** (`cnpg.backup.*`, guarded, CI-asserted).
- Enabled in production: **not yet** - needs the bucket prefix and the
  credentials secret in the `sextant` namespace.
- **This procedure has never been executed.** Until somebody restores into a
  scratch namespace and reveals a key from the restored database, this
  document is a plan and not a proof. That is the acceptance step; it is
  cheap and it is the only thing that turns the rest of this page into a
  guarantee.
