# E2e-2 findings — integrations on real hardware (2026-07-29/30)

What broke, why, and what changed because of it. Every item here was found
on a real laptop (test15, Lenovo T495s) imaged through the inspoelstraat,
not in a test. The acceptance test in the overlay
(`nix/tests/integrations-acceptance.nix`) now guards items 1-5.

## The one that mattered: a device frozen since install

**Symptom.** A freshly imaged laptop never received fleet config. The
console showed it online and checking in, the rollout sat at
"0/1 healthy on target" for hours, and nothing said why.

**Cause chain.** The device generates its own SSH host key at install. That
key was not an age recipient of the overlay's secrets, so on the device
EVERY secret failed to decrypt ("no identity matched any of the
recipients"). The agenix activation script exited non-zero, so the whole
activation failed, so comin correctly refused to switch to a broken
generation - and kept refusing, forever. The device stayed on its
install-time generation with no integrations, indefinitely, while the
engine waited in silence.

**Why the engine was right and still wrong.** It never lied: the ring was
genuinely not converged and it never promoted. But it waited without a
word, and "1 away" on the board reads as "almost there" rather than "this
device cannot get there".

**Fixed by** (Sextant 0.67.0):
- The station reports the device's host public key with the installed
  status; the console records it on the asset record (`ITAM.HostKeyID`)
  and `scripts/rekey-secrets.sh` re-encrypts the overlay's secrets for
  every enrolled device. A device is now decryptable from birth.
- A wave promoted past `rollout.StallWindow` without converging raises a
  fleet-level incident naming the devices it waits on and pointing at the
  activation log. An online device on a revision the repo cannot place in
  its history is called out as following an unknown source.

## Wazuh: five packaging defects in one module

The vendor package assumes an FHS system and a running installer. Each
defect below hid the next; all were invisible until the unit ran on real
hardware.

1. **`awk: command not found`** — `wazuh-control` is a shell script calling
   awk/sed/ps. Fixed: `path = [ gawk gnused procps coreutils ]`.
2. **`mkdir '../queue': read-only file system`** — the agent resolves its
   queue relative to the working directory. Fixed:
   `WorkingDirectory = "/var/lib/ossec"` plus creating the runtime dirs
   rsync deliberately excludes.
3. **`Invalid user '' or group 'wazuh'`** — the binaries are compiled with a
   fixed runtime user that NixOS does not create for a binaries-only
   package. Fixed: declare the user and group.
4. **`Invalid server address found: 'MANAGER_IP'`** — the unit exported the
   STORE bin directory on PATH, and every wazuh binary derives its home
   from its own location, so the store copies read the store's placeholder
   config and could not write a PID file. Fixed: `PATH=/var/ossec/bin`,
   the state tree, never the store.
5. **Enrollment refused by the manager.** Three separate causes, in order:
   port 1515 was in the compose file but not on the running container (a
   `docker restart` reuses the creation-time port map; `docker compose up
   -d` recreates it); `use_password` was `no` in the container's LIVE
   `/var/ossec/etc/ossec.conf`, which is a DIFFERENT file from the mounted
   `wazuh_manager.conf`; and the password differed by a trailing newline
   between device and manager.

**Result.** Enrollment works - the device registers as an agent on the
manager and holds a `client.keys`. Still open: `wazuh-agentd` rejects
`etc/ossec.conf` with "Error reading XML file (line 0)" even with the
package's own 191-line template substituted in. Ruled out: XML validity
(no BOM, no CR, no NUL, parses), the symlink path, the enrollment-block
injection, missing sections, and file ownership. Notable: the same binary
read a minimal config fine BEFORE `client.keys` existed, so the next step
is what agentd parses additionally once it holds a key (`etc/shared/`
holds only cis_*.txt - no `agent.conf`).

**Do not use `systemctl is-active wazuh-agent` as a health signal.** It
legitimately fails until enrollment succeeds, on a device and in a VM.

## Identity (SSSD): three layers, each silent

1. **`SSSD is offline` with a perfectly reachable directory.** The config
   demanded a certificate (`ldap_tls_reqcert = demand`) on a plain
   `ldap://` URI, and SSSD additionally refuses password auth over an
   unencrypted channel without an explicit opt-out. Fixed: the certificate
   policy follows the transport - strict for `ldaps://`, relaxed for a
   plain URI that is only reachable over the WireGuard mesh (the channel is
   already encrypted and authenticated at the network layer).
2. **The mesh route was not distributed to the device's group.** The
   NetBird network route for the cluster subnet listed only the infra
   group, so `10.43.0.0/16` resolved via the LAN gateway and the directory
   was unreachable. A per-peer mesh IP worked the whole time, which made
   the failure look like an LDAP problem rather than a routing one.
3. **The read-only bind account could bind but not read.** OpenLDAP's ACL
   ended in `by * none`, so every search returned `err=32` (noSuchObject)
   - indistinguishable from an empty directory, and it cost an hour on the
   wrong hypothesis ("the directory was never seeded"). Fixed: an ACL
   granting `cn=sextant-ro` read on the subtree. **Still to do: capture
   that ACL in the bb-ldap repo** - it is a live `cn=config` change today.

Also: `offline_credentials_expiration` is a `[pam]` option, not a domain
option. It sat in the domain section where sssd's validator rejects it, so
offline login validity was silently not configured.

## Boot experience

Quiet boot already existed in the core (`boot-plymouth-bzk`: plymouth,
bgrt theme, `quiet`/`splash`, `consoleLogLevel = 0`) and was simply never
added to the fleet's workplace stack. No upstream change was needed.

## Process lessons

- **`nixos-rebuild` without `--refresh` serves a cached evaluation.** Three
  rebuilds in a row silently built the previous revision, which read as
  "the fix does not work". Always `--refresh` when chasing a fix through a
  branch.
- **Ring branches are engine-owned.** A manual push to `rings/<group>` was
  force-overwritten by the next promotion mid-debug. Imaging installs from
  `main`; the engine's first promotion draws a device into the ring.
- **Admin SSH access is a debugging prerequisite, not a nice-to-have.**
  Until the operator's key was on the device, every diagnostic step was
  "type this, paste that back". The last three defects were found within
  minutes of having a shell.
- **A guard that cannot be observed is not a guard.** Several fixes here
  are about making a correct-but-silent system say what it is waiting for.
