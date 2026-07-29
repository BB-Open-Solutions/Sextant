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

## Wazuh: six packaging defects in one module

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

6. **One directory wearing three error messages.** The unit used systemd's
   `StateDirectory`, which re-applies the directory's ownership from the
   unit's own identity before EVERY `Exec` command. The unit runs as root;
   the wazuh binaries drop privileges to the `wazuh` user. So systemd put
   `/var/lib/ossec` back to `0750 root:root` after the preStart's
   `chown -R`, and the runtime user could not TRAVERSE its own home. Every
   read below it then failed with a message naming something other than
   permissions:
   - `Error reading XML file 'etc/ossec.conf': (line 0)` - never an XML
     problem at all. Hours went into validating the XML, ruling out
     BOM/CR/NUL, and substituting the package's own 191-line template.
   - `(1402): Authentication key file 'etc/client.keys' not found` while
     that file sat there with 80 bytes in it.
   - `(1210): Queue 'queue/sockets/queue' not accessible: 'Permission
     denied'` - the only one of the three that told the truth.

   Fixed by owning the directory with a tmpfiles rule instead of
   `StateDirectory`, so nothing chowns it back. Proven on the device: after
   a single `chown -R wazuh:wazuh`, agentd read its keys and its config and
   reported `Connected to the server ([siem.bb-open.com]:1514/tcp)`.

**Result.** The agent enrols, reads its configuration and connects to the
manager.

**The lesson worth more than the fix.** An earlier version of the
acceptance test measured this exact ownership (`root:root 0750` ten seconds
into `ExecStart`, wazuh user unable to traverse) and wrote it off in a
comment as cosmetic - "asserting the chown's intent here would only encode
a no-op". It was not cosmetic; it was the whole bug. A test that observes
an anomaly and argues it away is worse than one that never looked, because
it leaves a note telling the next reader not to bother. The test now
asserts what the binaries actually need: that the wazuh user can read the
config and write the queue.

**Do not use `systemctl is-active wazuh-agent` as a health signal.** It
legitimately fails until enrollment succeeds, on a device and in a VM.

## Identity (SSSD): four layers, each silent

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
   wrong hypothesis ("the directory was never seeded"). A bind that can
   authenticate but not read is worse than a bind that fails: it lies about
   the directory's contents. Fixed live, and now captured declaratively in
   the bb-ldap repo along with the `cn=sextant-ro` account itself (osixia
   bootstraps custom LDIFs only on a volume's first start, so that commit
   is for rebuilds and new tenants, not a deploy action).

4. **SSSD resolved the user; the rest of the system could not see it.**
   With all three layers above fixed, `sssctl user-checks bbuijs` returned
   the full record - uid 10001, gid, gecos, home directory - while
   `getent passwd bbuijs` stayed empty, and stayed empty for four hours.
   Cause: `nsncd`, the NSS caching daemon, dlopen's `libnss_sss` once and
   caches its answers, and nixpkgs restart-triggers *sssd* on a settings
   change but never the daemon in front of it. It kept answering out of a
   world that no longer existed. `systemctl restart nscd` fixed it
   instantly. Fixed permanently with a restart trigger on the identity
   settings.

   This one is worth remembering as a diagnostic habit: **every tool that
   asked sssd said "fine".** `sssctl` talks to sssd over its own socket and
   bypasses NSS entirely, which is exactly the layer that was broken. The
   only signal that told the truth was the one a real login uses.

Also: `offline_credentials_expiration` is a `[pam]` option, not a domain
option. It sat in the domain section where sssd's validator rejects it, so
offline login validity was silently not configured.

Two things that looked like defects and were not, recorded so nobody
re-investigates them: `/etc/pam.d/gdm-password` contains no `pam_sss` line
(it `substack`s `login`, which has four), and `/etc/sssd/sssd.conf` shows
mode 777 to `stat` (that is the symlink; the file it points at is
`0600 root:root` outside the nix store, so the bind password does not leak).

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
- **Test the layer the user traverses, not the layer you suspect.** Two of
  the hardest defects here hid behind a healthy-looking subsystem: sssd
  answered its own socket correctly while NSS was blind, and wazuh's
  binaries reported a config-parse error for what was a directory
  permission. In both cases the tool closest to the suspect said "fine".
- **An anomaly you argue away in a comment is a defect you shipped.** The
  acceptance test measured wazuh's wrong ownership, explained why it did not
  matter, and moved on. It mattered completely. If a test notices something
  it cannot explain, the finding belongs in the assertion, not the prose.
