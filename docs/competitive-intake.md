# Competitive intake — what three neighbouring projects do that we do not

Three products were read in full (source, not marketing) in August 2026:

| Project | Who | Licence | Size | What it is |
|---|---|---|---|---|
| [Sécurix](https://github.com/cloud-gouv/securix) | DINUM (FR) | MIT, alpha | 146 files, ~8.3k lines Nix/MD | Sovereign NixOS secure workstation, ANSSI-driven, per-agent images |
| [clan-core](https://git.clan.lol/clan/clan-core) | clan.lol | MIT, unstable | 1120 files, ~8.4 MB | Nix framework to manage a group of machines; CLI + library, no server |
| [Bor](https://github.com/VuteTech/bor) | Vute Tech LTD | LGPL-3.0 | 414 files, ~37.5k lines Go + ~17.8k TSX | Server/agent desktop policy management for RPM/DEB/Arch Linux |

This document is the intake list: **everything from those three that could
plausibly earn a place in Sextant or in the DAWO-NixOS core.** It is not a
plan. Items are scored and then either move into `roadmap.md` with a trigger,
into `1.0-fit-gap.md` if they turn out to belong to 1.0, or are rejected here
with the reason.

Items that duplicate something already built or already scheduled were dropped
during triage and are listed at the bottom under "Triaged out", so the question
does not come back.

## How to read the table

- **Target** — `sextant` (control plane), `core` (DAWO-NixOS device flake), or
  `both`.
- **Value / Effort** — 1–5, first pass, to be re-scored in the session. Value
  is *to a municipality buying this*, not to us.
- **Slot** — *proposed* release, before scoring. Once an item is scheduled,
  **`roadmap.md` is authoritative and this column is history.** The two must
  not be kept in step by hand; that is how the previous roadmap drifted from
  the fit-gap. `2.0` means it changes a contract or an ADR; `reject` means it
  is written down so it stays rejected.
- **Conflict** — names the ADR or design rule the item argues with. An item
  with a conflict is not automatically out; it needs the argument won first.

---

## A. Compliance and evidence

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| A1 | Rules as data with a forced exclusion rationale | Sécurix | both | 5 | 3 | 1.2 | — |
| A2 | Machine-readable compliance report artifact per device | Sécurix | core | 4 | 2 | 1.2 | — |
| A3 | On-device compliance check binary | Sécurix | core | 3 | 2 | 1.3 | — |
| A4 | OCSF audit sink | Bor | sextant | 4 | 2 | 1.2 | — |
| A5 | CEF + syslog audit sink | Bor | sextant | 3 | 1 | 1.2 | — |
| A6 | Crypto-compliance reference doc with deployment checklist | Bor | both | 4 | 2 | 1.1 | — |
| A7 | FIPS-validated build mode for the server binary | Bor | sextant | 2 | 2 | unsched | — |

**A1.** Sécurix models each ANSSI rule as data: `{ anssiRef, severity,
category, tags, config, checkScript }`, with levels
`minimal|intermediary|reinforced|high` and categories `base|client|server`. The
part worth stealing is not the rules — most of their ruleset is still `TODO` —
it is the **exclusion taxonomy**. A rule that is off must be off for a named
reason: excluded by tag, by category, by level, by architecture, or by an
explicit `exceptions.<R>.rationale`. Anything else is reported as
`via = "unknown"` and labelled *non-compliant*, because it means somebody set
`enable = false` behind the governance system's back.

We already have policies with BIO/ISO annotations and a comply-or-explain
register. What we do not have is the rule that **silence is a finding**.

**A2/A3.** They emit `system.build.complianceReportDocument` — a JSON document
under a versioned schema id (`org.securix.anssi-compliance.v1`) — and ship
`anssi-nixos-compliance-check` on the device itself. Two different audiences:
the JSON feeds our evidence export; the binary lets a support engineer or an
auditor prove posture standing in front of the machine, without the console.

**A4/A5.** Bor emits every audit event to four sinks: database, syslog, CEF and
OCSF (`audit/ocsf.go`, ~240 lines). The class mapping is the work — 6003 API
activity, 3002 account change, 1001 file activity — and it is directly
reusable thinking. Our evidence export is for auditors; this is for the SOC.

**A6.** `docs/SECURITY.md` in Bor maps every algorithm to FIPS 140-3, BSI
TR-02102-1/2, ANSSI RGS, ETSI TS 119 312 / EN 319 412, NIS2/ENISA and NCSC UK,
and closes with a deployment checklist. This is the document A1 is supposed to
produce evidence *for*, and Bor shows how thick it has to be.

---

## B. Secrets and keys

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| B1 | Secret generators instead of secret files | clan | both | 5 | 4 | 2.0 | — |
| B2 | `neededFor` staging: partitioning / activation / users / services | clan | core | 5 | 3 | 1.2 | — |
| B3 | Declarative rotation via a validation hash | clan | both | 4 | 2 | 2.0 | — |
| B4 | Fleet-wide vs per-device secrets (`share`) | clan | both | 3 | 2 | 2.0 | — |
| B5 | Generator dependencies (secret pipelines) | clan | both | 3 | 2 | 2.0 | — |
| B6 | `deploy = false` — secret exists, never reaches the device | clan | both | 4 | 1 | 1.2 | — |
| B7 | Generators run sandboxed | clan | both | 3 | 2 | 2.0 | — |
| B8 | Warn when a non-secret generated file has non-default mode | clan | core | 2 | 1 | 1.1 | — |
| B9 | PKCS#11 / HSM for the cell CA key | Bor | sextant | 3 | 3 | unsched | — |
| B10 | Recovery keyslot enrolled at install, `/recovery` partition | Sécurix | core | 4 | 3 | 1.2 | — |
| B11 | Secrets that only decrypt with a hardware token present | Sécurix | both | 5 | 3 | 1.2 | see I6 |

**B1 is the largest single idea in the three repositories.** clan does not
store secrets, it stores the *derivation* of secrets: a generator declares
`files`, `prompts`, `runtimeInputs` and a `script`, and `clan vars generate`
creates, encrypts and distributes whatever is missing. A new device needs no
"remember to add the recipient" step, because there is no manual step to
forget. We solved the recipient half already (a newly imaged device's host key
is registered automatically); this is the other half.

**B2 is the immediately useful piece and can land without B1.** Each generated
file declares *when* it must exist on the target:

- `partitioning` — deployed before disko runs, so a LUKS key is managed
  declaratively at install time
- `users` — deployed before useradd, into `/run/secrets-for-users`, so password
  hashes come from the vault
- `activation` — before `nixos-rebuild`/`nixos-install`
- `services` — the default

Our recovery-key escrow stores a key after the fact. This puts the key in the
right place before the disk exists. They are complementary, not competing.

**B6** pairs with B10 and with emergency access (C5): a secret that provably
never lands on the endpoint is a different risk class from one that does.

**B10.** Sécurix's `securix_v2` disko layout sets `enrollFido2 = true` and
`enrollRecovery = true` on the LUKS container and carves a separate 2 GiB
`/recovery` partition. That is the on-disk half of break-glass; escrow is the
off-disk half.

---

## C. Device security controls (DAWO-NixOS core)

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| C1 | Per-user egress killswitch over an interface allowlist | clan | core | 5 | 2 | 1.1 | — |
| C2 | *(moved to theme I — hardware-token admin devices)* | Sécurix | core | — | — | — | — |
| C3 | *(moved to I5)* | Sécurix | core | — | — | — | — |
| C4 | SSH host and user keys sealed in the TPM | clan/Sécurix | core | 4 | 3 | 1.2 | — |
| C5 | Emergency initrd access with a vault-only password | clan | core | 4 | 1 | 1.1 | — |
| C6 | A working auditd ruleset | Sécurix | core | 3 | 2 | 1.1 | — |
| C7 | machine-id and stateVersion as generated values | clan | core | 3 | 1 | 1.1 | — |
| C8 | Non-root `upgrade` for a device-local operator group | Sécurix | core | 3 | 2 | 1.3 | ADR: pull-only |
| C9 | Tamper-restore for managed files outside the Nix store | Bor | both | 3 | 2 | 1.3 | — |
| C10 | Versioned filesystem layouts, migration explicitly unsupported | Sécurix | core | 4 | 2 | 1.1 | — |
| C11 | KDE Kiosk + polkit lockdown as fleet settings | Bor | core | 4 | 3 | 1.2 | — |
| C12 | Browser policy surface as fleet settings | Bor | core | 4 | 3 | 1.2 | — |
| C13 | Host firewall policy as a fleet setting | Bor | core | 3 | 2 | 1.2 | — |

**C1 is the highest value-to-effort item on this list.** clan's
`nixosModules/user-firewall` drops all egress from `isNormalUser` accounts
except over an allowlist of interfaces (`lo`, `wg*`, `zt*`, `tun*`, `tap*`,
`tailscale*`, `ipsec*`, `mycelium`, …), with `exemptUsers` for exceptions, both
iptables and nftables, an assertion that the firewall is on, and a VM test in
`checks/user-firewall`. This is a VPN killswitch, and it is **exactly the
`hardening-egress-deny` that DAWO-NixOS already names as its next hardening
block** — written, tested, and MIT-licensed.

**C5.** `xkcdpass` generates a recovery password, its hash goes into
`boot.initrd.systemd.emergencyAccess`, and the password itself carries
`deploy = false` so it stays in the vault. A device that will not boot can be
recovered without a shared password living on every machine. Roughly twenty
lines.

**C7.** `/etc/machine-id` and `system.stateVersion` become generated values, so
machine identity is deterministic and stateVersion is pinned at first
generation instead of drifting with the release. Two classic NixOS traps,
closed structurally.

**C8 conflicts with the pull-only rule and must be argued, not assumed.** The
observation behind it is real: a pilot user today cannot update anything
themselves. Sécurix ships an `upgrade` command usable by a device-local
`operator` group, with a man page. Whether that is a remote command channel is
the question — it is initiated *on* the device, by a local user, and still
pulls. That may be inside the rule rather than outside it.

**C10** is the shape for roadmap item #49. Sécurix versions its disk layouts
(`securix_v1`, `securix_v2`, `office_v1`) and states plainly that migration
between layouts is unsupported — reinstall. Our two profiles both hardcode
`/dev/nvme0n1`; the fix is `/dev/disk/by-id`, and the modelling lesson is to
version the layout while we are in there.

**C11–C13.** Bor's policy schemas are a ready-made catalogue of *what is worth
locking on a Linux desktop*: KDE Kiosk (`kconfig` under `/etc/xdg` plus KCM
module restrictions), polkit rules, dconf keys, browser policies for
Firefox/Chrome/Chromium/Edge/Thunderbird including the Flatpak paths, and
firewalld. We have GNOME dconf hardening and nothing for KDE, polkit or the
browsers. The *content* transfers even though the mechanism does not — for us
these become annotated `dawo.*` options that the catalog renders by itself.

---

## D. Enrollment and identity

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| D1 | Kerberos/SPNEGO enrollment against AD or FreeIPA | Bor | sextant | 4 | 3 | 1.2 | — |
| D2 | Server forces the certificate CN, CSR subject discarded | Bor | sextant | 4 | 1 | 1.1 | — |
| D3 | Revocation checked per connection, surviving device deletion | Bor | sextant | 4 | 2 | 1.1 | — |
| D4 | Short-lived device credentials with automatic renewal | Bor | sextant | 3 | 3 | 1.2 | — |
| D5 | Built-in console MFA (TOTP + WebAuthn) | Bor | sextant | 2 | 4 | reject | — |
| D6 | Claimed identity must match the authenticated credential | Bor | sextant | 4 | 1 | 1.1 | — |

**D2/D6 are one-line security properties with a test each, and they generalise
past certificates.** Bor's `SignCSR` discards the CSR subject entirely and
forces the CN to the server-assigned node name — with
`TestSignCSR_OverridesCommonName` proving that a CSR claiming to be
`victim-node` still gets issued as `attacker-node`. And
`requireClientIdentity` rejects any request whose `client_id` does not match
the authenticated credential's CN. Both apply verbatim to our per-device
credentials: **never trust an identity the client supplied.**

**D3.** Their migration 27 is called `revocation_survives_node_delete` —
somebody worked out that deleting a device must not quietly un-revoke its
credential. Worth checking whether ours has the same property.

**D5 is rejected on purpose.** Console auth is OIDC by design and the IdP owns
MFA. Building a second authenticator would be a second thing to get wrong.
Recorded here so the "but Bor has WebAuthn built in" question has an answer.

---

## E. Imaging and installer

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| E1 | Installer built as a library from the target closure | Sécurix | core | 5 | 4 | 1.2 | — |
| E2 | TUI autoinstall with confirm and a real-disk guard | Sécurix | core | 3 | 2 | 1.2 | — |
| E3 | Idempotent-autoinstall VM test | clan/Sécurix | both | 4 | 3 | 1.1 | — |
| E4 | The instance serves its own signed agent packages | Bor | sextant | 4 | 3 | 1.2 | — |
| E5 | Installer announces itself as a Tor onion service | clan | core | 2 | 2 | reject | ADR: pull-only |

**E1.** `lib/default.nix` in Sécurix composes an installer *from* the target
system: `buildUSBInstallerISO` and `buildNetbootInstaller` take the machine's
own closure and emit an image whose install script runs disko's format and
mount scripts, asserts `/mnt` is a real mountpoint on a persistent disk before
continuing, installs, then runs `sbctl create-keys` and `sbctl enroll-keys`,
generates TPM2-backed host keys and age host keys, and calls a
`postInstallScript` hook. Our Secure Boot wizard is READY but its open gap is
the on-hardware ceremony — this is that ceremony, in the image, with the
`postInstallScript` being the obvious place to call back to the console.

**E4.** Every Bor instance hosts its own apt/dnf/zypper repositories and a
deploy wizard that emits one copy-paste script: trust the CA, add the signed
repository, install, enroll, start. Managed nodes never need internet. The same
shape fits our station and Rust agent, and it is the honest answer to an
air-gapped municipality.

**E5 rejected.** Reaching a machine behind NAT with no known IP is a real
problem and this solves it, but a Tor onion service on the installer is a
listening remote channel. If the problem needs solving, solve it in the
provisioning station on the local network.

---

## F. Reachability and break-glass

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| F1 | Transport ladder with priority and automatic fallback | clan | sextant | 2 | 4 | reject | ADR: pull-only |
| F2 | P2P SSH break-glass, armed like the wipe intent | clan | both | 3 | 4 | 2.0 | ADR: pull-only |
| F3 | Documented emergency direct-target override | clan | sextant | 2 | 1 | 1.3 | — |

clan configures several transports at once and works down a priority list —
`p2p-ssh-iroh` 3000, `internet` 2000, `wireguard` 1000, `zerotier` 900,
`mycelium` 800, `tor` 10 — falling back automatically until one connects. It is
elegant, and it exists because **clan must be able to reach a machine in order
to deploy at all.** We must not, and that is the product's central claim.

**F1 is therefore rejected**, and F2 is the only version worth arguing:
peer-to-peer SSH that is off by default, armed per device the way the wipe
intent is armed, expires, and is audited. clan's own documentation warns that
enabling it exposes the SSH daemon to anyone on the Iroh network — so it would
need the threat model reopened, which is why it sits at 2.0 and not earlier.

---

## G. Console and product

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| G1 | Richer schema annotations for generated forms | Bor | both | 4 | 2 | 1.1 | — |
| G2 | Versioned config export envelope that refuses unknown types | Bor | sextant | 4 | 2 | 1.2 | — |
| G3 | Per-item compliance results, not one verdict per device | Bor | sextant | 4 | 2 | 1.2 | — |
| G4 | Notify the logged-in user when policy changes, with cooldown | Bor/Sécurix | both | 3 | 2 | 1.2 | — |
| G5 | Backup and restore as a first-class abstraction | clan | both | 4 | 4 | 2.0 | — |
| G6 | Central journal shipping | Sécurix | core | 3 | 2 | 1.2 | — |
| G7 | Per-device metrics including power draw | Sécurix | core | 2 | 2 | unsched | — |

**G1.** We already generate the settings surface from annotated `dawo.*`
options — that part we do not need. What Bor's `(bor.ui.field)` annotation adds
is vocabulary: `group` for sections, `label`, `description`,
`int_options`/`string_options` for enums *with human labels*, and
`chrome_only` — a flag marking an option that is a no-op in some contexts, so
the UI can say so instead of silently lying. That last one generalises: an
option that does nothing on GNOME, or nothing without Secure Boot, should be
able to say so in the catalog.

**G2.** Bor's export/import uses a `bor.dev/v1` envelope, round-trips through
protojson, and **refuses to import a policy type this build does not know** —
because a policy that cannot round-trip the schema could not be edited or
enforced afterwards. Directly relevant to moving configuration between cells.

**G3.** Their agent reports per-item compliance (`{ schema_id, key, status,
message }`) stored as `items_json`. Our compliance baseline view produces one
aggregate verdict per device. Per-item is the difference between "this device
fails" and "this device fails these two keys".

**G4.** Both Bor and Sécurix notify the logged-in user, with a cooldown, that
policy changed and what they must do about it ("log out and back in", "restart
your browser"). We converge silently. On a pull model with rings, the user is
the last person to know, which is precisely backwards.

**G5.** clan models backup as `clan.core.state` (folders plus
`preBackupScript`/`postRestoreScript` so a service can dump a database or stop
itself) against `backups.providers` (commands for `list`/`create`/`restore`).
borgbackup and localbackup implement the same interface. **We have no backup
story at all**, and for a municipal fleet that is a question we will be asked.

---

## H. Engineering practice

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| H1 | REUSE/SPDX headers plus a licensing CI check | Sécurix | both | 3 | 1 | 1.1 | — |
| H2 | Frontend design-token guardrail with a hex ratchet | Bor | sextant | 3 | 1 | 1.1 | — |
| H3 | Deprecations that carry their own migration snippet | clan | both | 4 | 1 | 1.1 | — |
| H4 | Container test driver alongside VM tests | clan | core | 3 | 3 | 1.2 | — |
| H5 | Guard against architecture docs drifting from the code | Bor | both | 3 | 2 | 1.1 | — |

**H2.** `scripts/check-frontend-tokens.sh` bans legacy PatternFly v5 CSS
variables outright and keeps a **ratchet** on raw hex colour literals: the
multiset of `{file, value}` may not grow beyond a committed baseline, so
existing debt passes and new debt fails. Twenty lines of bash, keyed on counts
rather than line numbers so it survives unrelated edits.

**H3.** clan's renamed options carry `mkRenamedOptionModule`, and its
consistency assertion for a deprecated vars backend **puts the migration
snippet in the failure message**. The error tells you what to write.

**H5 is a lesson rather than a feature.** Bor's `docs/ARCHITECTURE.md`
contradicts Bor's own code: it says agent keys are RSA-2048 and certificates
last ten years, where the code enforces a 3072-bit minimum and issues for
ninety days, and it lists as "future" several things that shipped. A reader who
trusts the architecture document gets a picture that is both stale and less
secure than reality. Our `architecture/` directory is larger than theirs.

---

## I. Hardware-token admin devices

An **admin device** is a separate product shape, not a setting: the machine an
operator uses to administer the fleet, held to a standard an ordinary
workplace laptop is not. Sécurix is built around this idea — it is a *poste
d'administration sécurisé* first and an office desktop second — and its
YubiKey work is the most transferable part of it.

The proposition is one token doing four jobs off one enrollment ceremony:
unlock the disk, log in, escalate privilege, and authenticate outward. Sécurix
ships two of the four and points at the rest.

| ID | Item | Source | Target | Value | Effort | Slot | Conflict |
|---|---|---|---|---|---|---|---|
| I1 | PAM U2F login, password demoted to fallback | Sécurix | core | 4 | 2 | 1.2 | — |
| I2 | Admin accounts that refuse to build without a registered key | Sécurix | core | 5 | 1 | 1.2 | — |
| I3 | `sudo` gated on the token, not just login | *new* | core | 5 | 2 | 1.2 | — |
| I4 | SSH via resident FIDO2 keys requiring a physical touch | *new* | core | 4 | 2 | 1.2 | — |
| I5 | LUKS unlock via a FIDO2 keyslot alongside TPM2 | Sécurix | core | 4 | 2 | 1.2 | — |
| I6 | Secrets encrypted to a YubiKey PIV slot | Sécurix | both | 5 | 3 | 1.2 | — |
| I7 | Key registration as fleet data, and who owns the mapping | *product* | sextant | 5 | 3 | 1.2 | — |
| I8 | A lockout matrix: four uses, four distinct recovery paths | *product* | both | 5 | 2 | 1.2 | — |
| I9 | Smartcard stack on the device (pcscd, ykman, yubikey-agent) | Sécurix | core | 3 | 1 | 1.2 | — |

### What Sécurix actually ships

**I1 — `modules/pam/u2f.nix`.** `securix.pam.u2f` takes an `appId`, an `origin`
and an attribute set of `username → [key handles]`, writes `/etc/u2f-mappings`,
and sets `security.pam.u2f` with `control = "sufficient"` and `cue = true`.
"Sufficient" is the honest word: a successful touch passes authentication, a
failure falls through to the password. Their README states the intent —
*"le mot de passe n'est qu'un mode secours"*.

Two details that bite if missed. The key handles must be generated with
`pamu2fcfg` using **the same appId and origin** the module configures, or the
key is simply not recognised; the module documents this and it is still the
most likely support call. And the default `pam://$HOSTNAME` scopes a key to one
machine — their own example (`pam://acme-corp-workstations`) shows the
alternative. See I7.

**I2 — `modules/admins/default.nix`.** This is the piece worth copying
verbatim, and it is fifteen lines. Admin accounts are declared as
`{ name, u2f_keys }`; the generated user is `isNormalUser` plus `wheel` and
**carries no password field at all**. Guarding it is an assertion:

> `PAM U2F is required to use the local admin accounts. There is no password.`

So a configuration that grants somebody administrative access without
registering a hardware key does not build. Not a warning, not a lint — an
evaluation failure. That is the correct severity, and it is the pattern our
`riskClass = foundation` options should arguably borrow.

**I6 — `modules/vpn/wireguard/default.nix`.** The surprise. The WireGuard
private key is not stored in plaintext anywhere: it is age-encrypted, and the
age identity lives in a YubiKey PIV slot. Decryption is
`ykman piv objects export … | age -d -i <(age-plugin-yubikey -i --slot …)`,
and the module generates the identity on first use if the slot is empty.

The property is stronger than anything in our secrets story today: **the
secret cannot be decrypted without the physical token in the machine.** Not
"protected by a passphrase", not "readable by root" — absent the token, the
ciphertext is inert. For an admin device holding fleet credentials that is
exactly the right guarantee, and it composes with the whole B series: a vars
generator (B1) whose output is encrypted to a PIV slot rather than to a host
key.

**I9 — `modules/security-keys.nix`.** The unglamorous enabling layer:
`yubikey-personalization` and `yubikey-manager` as packages *and* as udev
rules, `services.pcscd.enable` for smartcard access, `services.yubikey-agent`.
GPG agent is deliberately off with a comment saying they have no need for GPG
keys yet — a decision recorded rather than a gap.

### What Sécurix does not do, and where the value is

**I3 — the token gates login but not `sudo`.** Their PAM work covers
authentication to the session; privilege escalation still takes a password
(and their admin accounts have none, so the practical effect deserves
checking on real hardware). For an admin device this is backwards: the moment
that matters is `sudo`, not the morning login. `security.pam.services.sudo`
with U2F required — not sufficient, *required* — is the control worth having,
and it is a small module.

**I4 — SSH is agent-only.** `yubikey-agent` is enabled, but nothing wires up
resident FIDO2 SSH keys (`sk-ecdsa-sha2-nistp256@openssh.com`,
`sk-ssh-ed25519@openssh.com`), which is the form where the private key cannot
leave the token and every authentication needs a deliberate touch. For an
operator SSHing into fleet infrastructure that is the difference between a
stolen laptop being an inconvenience and being an incident.

**I5.** Their `securix_v2` layout sets `enrollFido2 = true` on the LUKS
container, so the same token that logs you in unlocks the disk. We have TPM2
auto-unlock bound to PCR 7, which is the right default for a workplace laptop
— it is invisible and it survives the user. For an admin device the trade-off
inverts: you *want* the unlock to require something the thief does not have.
Both should exist; the device profile chooses.

### The product questions (I7, I8) — these are ours, not theirs

**I7. Who owns the user → key-handle mapping?** A U2F key handle is public
data, so it can live in the fleet document as an ordinary setting — no vault
needed. But it is per-user *and* per-appId, and that forces a decision we have
not made:

- **appId per host** (`pam://$HOSTNAME`, the upstream default) — a key must be
  registered against every device it may unlock. Safest, and unusable at any
  scale.
- **appId per fleet** (`pam://dawo-admin`) — one enrollment, works on every
  admin device in the organisation. One stolen token also works on every admin
  device, which is precisely what the token's own PIN and presence
  requirement exist to answer.

For admin devices the fleet-wide appId is the practical choice, and the
argument for it should be written down rather than defaulted into.

The second half: the ceremony. `pamu2fcfg` must run with the key present and
the right appId, and it emits a string. Three possible owners — the
provisioning station during imaging, a self-service page in the console where
an operator registers their own key, or an administrator pasting the string.
Self-service is the only one that scales and the only one where the private
key never passes through anyone else's hands, but it means the console needs a
page that produces the exact `pamu2fcfg` invocation for our appId.

**I8. Every one of the four uses is a lockout, and they must not share a
recovery path.** This is the item that decides whether I1–I6 are a feature or
an outage:

| Use | Lost-token fallback | Must not be |
|---|---|---|
| Disk unlock | recovery keyslot (B10) + escrow | the same secret as login |
| Login | password, if the device keeps one | absent, per I2 |
| `sudo` | a second registered token, or a break-glass local admin | a password everyone knows |
| SSH | a separate key held elsewhere | on the same laptop |

Sécurix generates a LUKS recovery key at install (`enrollRecovery`) and we
already escrow recovery keys with audited reveals — so that column is close to
answered. The rest is not. The rule to hold: **an admin device requires two
registered tokens, or it requires a documented break-glass that is not itself
protected by the thing being recovered.** That is the same ordering trap
already recorded against OpenBao in the roadmap.

### How this reaches the console

Nothing here needs a new mechanism. It is an `dawo.adminDevice.*` block of
annotated options in the core, which the catalog renders by itself, plus one
new console surface for I7's self-service registration. The device profile —
"this group is admin devices" — is an ordinary group setting.

What it does need is the boundary decision from roadmap 1.1 (*"what stays in
the image and what Sextant composes"*), because an admin-device profile is the
first real case of a **named device class** rather than a pile of individual
settings.

---

## Triaged out — already built, already scheduled, or already decided

Recorded so these do not come back as "but project X has…".

| Seen in | Why it is not on the list |
|---|---|
| Granular RBAC permissions (Bor) | Already roadmap 1.2, issue #53, and our capability model is finer |
| Four-eyes approval (Bor has none; Sécurix none) | Already built |
| Ring/staged rollout | Nobody else has it; it is our differentiator |
| Compliance baseline view (Bor) | DONE per fit-gap 5b |
| Recovery-key escrow (Bor has none) | DONE per fit-gap 5b |
| Remote diagnostics (Bor) | DONE per fit-gap 5b |
| Crypto-wipe (Bor has none) | DONE per fit-gap 1.3 |
| LDAP/AD console auth (Bor) | Already an unscheduled item: SCIM inbound and LDAP direct-bind |
| Mobile console (nobody has one) | Already roadmap 1.3, issue #48 |
| Multi-tenancy (Bor lists it as planned) | Already roadmap, cells + reseller portal |
| Prometheus metrics on the server (Bor) | Already built |
| Delta sync with a revision counter (Bor) | Push-model machinery; devices pull here |
| Per-user image builds (Sécurix `mkTerminals`) | Deliberate difference: one image, the console handles the user |
| npins over flakes (Sécurix) | Settled; we are flakes |
| mdbook manual (Sécurix) | We have `docs/handbook` |

## Scoring session

Fill in the two number columns per row, then:

- **Value ≥ 4 and Effort ≤ 2** → propose for 1.1, needs a trigger written.
- **Value ≥ 4 and Effort ≥ 3** → propose for 1.2/1.3, needs an issue first.
- **Conflict non-empty** → the ADR argument is won *before* the item is
  scheduled, not after.
- **Anything scoring Value ≤ 2** → move to "Triaged out" with the reason, so
  it is answered rather than pending.
