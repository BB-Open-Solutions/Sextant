# Roadmap after 1.0

What 1.0 contains is decided elsewhere: `1.0-fit-gap.md` holds the scope and
the gate. This document starts where that one stops.

An earlier roadmap was deliberately deleted, because it restated the 1.0 scope
that the fit-gap already owned and the two drifted apart. This one does not
touch 1.0 at all. If something here turns out to belong in 1.0, it moves to the
fit-gap and leaves - it is never described in both. It says what comes next,
in what order, and - more usefully - **what forces each item**, because a
roadmap that is only a wish list gets reordered by whoever shouts loudest.

The same split applies to `competitive-intake.md`, which holds the reading of
Sécurix, clan-core and Bor: **the argument lives there, the schedule lives
here.** That document also records why `git log` on this file points at three
commits about unrelated work - see "Where this came from". Items below carry their intake ID so the reasoning is one link away
instead of repeated. An intake item not named here is not scheduled - it is
still being scored, or it was triaged out with a reason recorded in that
document.

Dates are deliberately sparse. Each release below names its **trigger**: the
thing that makes it urgent. Where a trigger has a date, the release inherits
it. Where it does not, the release ships when the work is done rather than on a
calendar we invented.

## How we work from 1.0 onward

Until now, changes went straight to `main` with the pre-push hook as the gate.
That was right for a project with one author and a fleet of two, and it stops
being right the moment somebody else can contribute.

From 1.0.0:

1. **An issue first.** It states the problem and how you would know it is
   fixed. Not the solution.
2. **A branch and a commit** that explains *why*, in the style the repository
   already uses. From 2026-08-08 the subject is a Conventional Commit and a
   hook plus CI enforce it, the same way DAWO-NixOS does. The rule was already
   written down; what changed is that it now runs. The body is unchanged and
   is still the part that matters.
3. **A merge request.** CI green, review, then merge.
4. **Releases are tags on `main`**, cut after the merge - never before. That
   rule came from Rutger and applies here for the same reason: a tag ahead of
   review is an unreviewed release.

The one exception is a production incident, and it is written down so it stays
an exception: fix forward, then open the issue and the merge request
retroactively, with the incident named in the commit.

## Where the code lives, from go-live onward

**Trigger: go-live.** Decided 6 August 2026.

`forgejo.bb-open.com` stays the repository we actually work in: commits,
CI and Flux all read from it, and that does not change. What changes is the
public side. **Codeberg becomes a second mirror alongside
`code.overheid.nl`**, both reflecting what happens on forgejo, and the
GitHub repository goes offline.

**Done on the repository side, 2026-08-10** (commit `223e906`): every
reference to `github.com/BB-Open-Solutions/Sextant` is gone - the table, the
quickstart clone line, the CONTRIBUTING text that pointed contributors there,
two handbook links and the issue-template redirect. Codeberg is named as the
public front door. Taking the GitHub repository itself offline is Bram's step
and is not done.

This is deliberately not a migration. Nothing about the Go module path, the
`.forgejo/workflows/` pipeline or the self-hosted nix runner moves, because
the place the work happens is not moving. Mirrors are push targets.

Two things still to settle, and neither blocks the mirror itself:

- **Whether Codeberg also runs CI.** A mirror does not need it, but a public
  repository that shows no build status invites the question. If it does,
  that is Woodpecker and a second runner with nix - the release workflow
  assumes one.
- ~~What the public mirror is called.~~ **Settled.** The Codeberg repository
  is `DAWO/DAWO-Sextant`; the old `DAWO/SextantFleet` URL redirects to it.
  Checked 2026-08-10.

Also worth knowing before somebody wires it up: `upstreamRepo` in the
HelmRelease points at `code.overheid.nl/MinBZK/DAWO-NixOS.git`, so the core
repository has its own mirror question, separate from this one.

## 1.1 - what Zaanstad hits first

**Trigger: the first machines that are not pilot laptops.** Everything here is
already known to be wrong; it simply has not mattered on a fleet of two.

- **Multiple drives, and stable disk addressing (#49).** Two disko profiles
  exist and both hardcode `/dev/nvme0n1`. A second drive is not partitioned,
  not encrypted and not wiped - so a crypto-wipe destroys the keys of one disk
  and leaves the other readable, which nobody expects from the word "wipe". And
  enumeration order is not a promise: two NVMe drives and "the first one" can
  move. `/dev/disk/by-id` is the fix. Core work, so fork and pull request.
- **App profiles in the console (#54).** The additive model already works;
  what is missing is the named, reusable profile. Requires agreeing the
  boundary with the DAWO core first - what stays in the image and what Sextant
  composes.
- **A garbage-collection policy for devices.** A one-day-old laptop already
  carried 101 dead store paths. Not a problem this year; certainly one in a
  fleet nobody prunes.
- **Admin devices as a named class (intake theme I).** Decided 2026-08-10:
  `admin` becomes a fifth entry in the overlay's `byClass`, alongside laptop,
  desktop, station and server. That is the whole mechanical change - the
  boundary question this was waiting on turned out to be two image-time
  namespaces and one class distinction, all counted in the intake.

  **This item carries its own trigger, and it has already fired.** Bram's
  laptop joins the fleet as the first admin device in the week of
  2026-08-10, and it arrives before any of the nine items exists: a managed
  NixOS machine holding the fleet's SSH keys, forge push rights, cluster
  credentials, secret identity and backup keys, protected like an office
  laptop. Six of the nine are value 4-5 at effort 1-2, so they should follow
  the machine rather than wait: I7 (key registration as fleet data) leads
  because the others read from it, then I2 (an admin account that refuses to
  build without a registered key), I3 (`sudo` behind the token), I8 (the
  lockout matrix), I1 (PAM U2F), I4 (SSH via resident FIDO2) and I5 (a FIDO2
  keyslot for LUKS, which is image-time under the existing `diskUnlock.`
  prefix).

  The rule to hold, and it is the same ordering trap already recorded against
  OpenBao: **two registered tokens, or a documented break-glass that is not
  itself protected by the thing being recovered.** I6 (secrets encrypted to a
  PIV slot) waits for 2.0 with the secrets model.
- **Make the Wazuh agent useful on NixOS.** The agent enrols and reports; most
  of what it then does is aimed at a distribution we are not running. Split by
  cause, because "does not work" covers two very different things here:

  *The engine works, the content is wrong.* The bundled CIS Debian policies
  check `dpkg` state and file modes on paths that are store symlinks, so SCA
  scores are noise; the FIM defaults watch an `/etc` that is a symlink tree
  rewritten wholesale on every generation; the active-response scripts shell
  out to `iptables` and to binaries that are not on the unit's PATH, so
  response fails without failing loudly. All of it is ours to fix, and the
  policy content is reusable at the next customer.

  *The data source does not exist.* Syscollector reads `dpkg`/`rpm` for its
  package inventory and NixOS has neither, so vulnerability detection reports
  a clean fleet for every device. That one is not fixable inside Wazuh: its
  matcher is fed by vendor advisory streams and nixpkgs does not publish one.
  It is answered instead by scanning the closure off-device, which is why the
  SBOM and CVE report were pulled forward before 1.0 rather than left here.

  The inversion worth building: on NixOS the signal equivalent to "packages
  were installed" is a change to `/nix/var/nix/profiles/system`. One watch
  there, correlated with a closure diff, is more precise than file-level FIM
  on a mutable distribution - a better answer than the one being ported, not
  a workaround.

  Two things to settle before spending weeks on it. Whether Wazuh 5.0 is GA
  and how enrolment works there, because `agent-auth` is reported removed and
  building on 4.x would carry a known expiry date. And the measurement behind
  the empty inventory, so the ISO 27002 mapping can say it was observed on a
  date rather than derived from how syscollector works.

  Not a concern here, and checked on 2026-08-08 rather than assumed: the
  overlay uses no impermanence or tmpfs root and the agent state lives on the
  ordinary root filesystem, so the re-enrolment loop that bites immutable
  deployments does not apply.

- **Deterministic machine identity (intake C7).** `/etc/machine-id` and
  `system.stateVersion` become generated values held with the device rather
  than whatever the installer happened to produce. On a fleet of two nobody
  notices; on a fleet that gets re-imaged, a machine-id that changes underneath
  the console is a device that looks new, and a stateVersion that drifts with
  the release is a migration nobody chose. clan closes both structurally and it
  is a handful of lines.

- **The disk layout gets a version while #49 is open (intake C10).** Sécurix
  versions its layouts and states plainly that migrating between them is
  unsupported - reinstall. That is the honest shape and it costs nothing to
  adopt now, while the profiles are being rewritten for `/dev/disk/by-id`
  anyway. Adopting it afterwards means an unversioned second layout already in
  the field.

- **Egress that stops at the tunnel (intake C1).** A device outside the
  building is what forces this, and the control is a per-user killswitch:
  ordinary user accounts reach the network only over an allowlist of
  interfaces, system services exempt. DAWO-NixOS already names this as its next
  hardening block; clan has it written, tested and MIT-licensed, so the work is
  porting plus a VM test rather than design. Core work, so fork and pull
  request.

- **Emergency access that is not a shared password (intake C5).** A device that
  will not boot in the field has no recovery story today that does not end in a
  password somebody remembers. A generated initrd recovery password, hash on
  the device and plaintext never leaving the vault, is about twenty lines and
  removes the temptation.

- **Never trust a claimed identity (intake D2, D3, D6).** Three properties to
  verify we already hold on per-device credentials, each cheap and each worth a
  test: the server decides a credential's subject rather than echoing what the
  client asked for; a claimed device id must match the authenticated
  credential; and revocation survives deleting the device. Bor names that last
  one in a migration called `revocation_survives_node_delete`, which is exactly
  the failure it prevents. If we already hold all three, the outcome is a test
  that says so - which is worth having anyway.

- **Guardrails that keep the repository honest (intake H1, H2, H3, H5).** Same
  trigger as the process change at 1.0.0: somebody else can contribute now.
  Four small things - REUSE/SPDX headers with a licensing check; the frontend
  design-token ratchet that fails only on *newly* introduced raw hex;
  deprecations whose failure message carries the migration snippet; and a check
  that `architecture/` has not drifted from the code. The last one is not
  theoretical: Bor's architecture document contradicts Bor's own code on key
  strength and certificate lifetime, and our `architecture/` directory is
  larger than theirs.

## 1.2 - governance that a municipality will ask for

**Trigger: the first customer with more than one administrator.**

- **Capability RBAC on directory groups (#53).** Today the model is
  Viewer/Editor/Owner scoped by group, which is the right shape and too coarse.
  Permissions on a small set of named capabilities - start a rollout, approve a
  wave, wipe a device, reveal a secret, change endpoint controls, change access
  itself - each bound to a directory group at a scope.
- **Four-eyes narrowed to where it earns its place.** The rule, not a list:
  *a change the gate and the test ring can prove is fine needs no second
  person; a change that removes the gate, removes the ring, or removes the
  ability to recover, does.* The mechanism already exists as `riskClass` in the
  catalog and is used on exactly two options today.

- **A reseller portal.** Cell provisioning stays manual for 1.0 - a
  template directory and a runbook, `cp` and `sed`, decided 2026-07-28
  and reconfirmed 2026-08-05. What replaces it is not a scaffolder for
  us but a portal where customers create and manage their own
  environments. That is a different product surface with its own
  tenancy boundary, so it waits until the cell shape has been proven by
  provisioning and retiring real ones by hand.

- **Admin devices as a named class (intake theme I).** More than one
  administrator is exactly the moment this stops being theoretical: the machine
  someone administers the fleet from should be held to a standard an office
  laptop is not. One hardware token doing four jobs off one enrollment - unlock
  the disk, log in, escalate privilege, authenticate outward.

  Sécurix ships two of the four and the second is the one to copy verbatim:
  admin accounts declared with their key handles, carrying no password field at
  all, guarded by an assertion so that granting somebody administrative access
  without a registered key *does not build*. An evaluation failure, not a
  warning. That severity is right, and it is the pattern our `riskClass =
  foundation` options should probably borrow.

  What they do not do is where the value is. Their token gates login but not
  `sudo`, and for an admin device the moment that matters is the escalation,
  not the morning login. And `yubikey-agent` is enabled without resident FIDO2
  SSH keys, the form where the private key cannot leave the token and every
  authentication needs a deliberate touch.

  Two decisions are ours before any of it is scheduled. **The appId scope**:
  per-host means re-registering a key on every machine, fleet-wide means one
  ceremony and one stolen token that works everywhere - fleet-wide is the
  practical answer and the argument for it gets written down rather than
  defaulted into. And **the lockout matrix**: four uses are four ways to lock
  yourself out, and they must not collapse onto one recovery path. The rule to
  hold is that an admin device requires two registered tokens, or a documented
  break-glass that is not itself protected by the thing being recovered - the
  same ordering trap already recorded against OpenBao below.

  This is also the first genuinely *named device class* rather than a pile of
  settings, so it depends on the image-versus-console boundary from 1.1.

- **Secrets that arrive before the thing that needs them (intake B2, B6, B10).**
  A generated file declares when it must exist: before disko runs, so a LUKS
  key is managed declaratively at install; before useradd, so password hashes
  come from the vault; or the ordinary case. Recovery-key escrow stores a key
  after the fact and stays; this puts one in place before the disk exists.
  Alongside it, the ability to declare that a secret exists in the vault and
  must **never** reach the device - which is what makes emergency access (1.1)
  and admin-device break-glass different risk classes from an ordinary secret.

- **Compliance that says why a control is off (intake A1, A2, A3, G3).** We
  have policies annotated with BIO/ISO controls and a comply-or-explain
  register. What Sécurix adds is that **silence is a finding**: a rule that is
  not enforced must be off for a named reason - excluded by scope, by level, or
  by an explicit exception carrying a rationale - and anything else is reported
  as unexplained and therefore non-compliant. With it come two artifacts: a
  machine-readable posture document per device, which is what our evidence
  export wants as input, and per-item results so a verdict reads "fails these
  two keys" rather than "fails".

- **Audit output a SOC can consume (intake A4, A5).** Our evidence export is
  built for an auditor reading it once. A customer with a security operations
  centre wants the same events continuously, in a format their tooling already
  parses: OCSF and CEF alongside syslog. The class mapping is the work and Bor
  has done it - roughly 240 lines of thinking we do not have to repeat.

- **Configuration that can move between cells (intake G2).** A versioned export
  envelope that round-trips through the schema and **refuses to import a type
  this build does not understand**, because a policy that cannot round-trip is
  one nobody can edit or enforce afterwards. The trigger is the second cell,
  which the reseller portal above makes certain.

- **Tell the user something changed (intake G4).** A pull model with rings
  means the person using the laptop is the last to know why their browser
  restarted. A notification naming what changed and what they must do about it,
  with a cooldown so it is not noise. Both Sécurix and Bor do this; we converge
  silently, which is precisely backwards.

## 1.3 - reach

**Trigger: an operator who is not at their desk when the notification arrives.**

- **The console on a phone (#48).** Server-rendered HTML with no JavaScript
  requirement is the best possible starting point and the viewport work is
  already partly done. The job is the six tables and the sidebar. Decide
  explicitly which actions belong on a phone - approving a wave and locking a
  lost device, yes; editing the whole settings tree, probably not.

- **A documented emergency override (intake F3).** When every normal path to a
  device has failed, the way in should be one written-down command with an
  audit trail, not improvisation at 23:00. clan has exactly this and labels it
  debugging-and-emergencies-only. Ours is an intent that already exists; what
  is missing is the page that says so.

## 1.4 - the desktop policy surface

**Trigger: the first customer who asks for a lockdown we cannot express.**
Browser policy is the usual first ask, and today the answer is "an engineer
writes a module", which is true and is not what a procurement conversation
wants to hear.

- **What is worth locking, taken from somebody who did the survey (intake C11,
  C12, C13).** Bor's policy schemas are a ready-made catalogue: KDE Kiosk
  (`kconfig` under `/etc/xdg` plus KCM module restrictions), polkit rules,
  dconf keys, browser policy for Firefox, Chrome/Chromium, Edge and
  Thunderbird including the Flatpak paths, and host firewall rules. We have
  GNOME dconf hardening and nothing for KDE, polkit or the browsers. The
  mechanism does not transfer - for us these are annotated `dawo.*` options the
  catalog renders by itself - but the *content* is the expensive part and it
  transfers completely.

- **Annotations rich enough for a real form (intake G1).** The catalog already
  generates the settings surface; what it cannot express yet is an enum with
  human labels, a section grouping, or the fact that an option is a no-op in
  some contexts. Bor's UI annotation carries a `chrome_only` flag for exactly
  that, and it generalises: an option that does nothing on GNOME, or nothing
  without Secure Boot, should be able to say so rather than silently lie.

## 1.5 - imaging that somebody else can run

**Trigger: the first site imaged by hands that are not ours.**

- **The installer becomes a library, not a recipe (intake E1, E2).** Sécurix
  composes an installer *from* the target system's own closure: the image runs
  disko's format and mount scripts, asserts `/mnt` is genuinely a mountpoint on
  a persistent disk before it continues, installs, enrolls Secure Boot keys
  with `sbctl`, generates TPM2-backed and age host keys, and then calls a
  `postInstallScript` hook. Our Secure Boot wizard is built and its open gap is
  the on-hardware ceremony - this is that ceremony, inside the image, and the
  hook is the obvious place to call the console back.

- **An idempotent install test (intake E3).** Both clan and Sécurix have one.
  Re-running an install must converge rather than half-succeed, and that is not
  a property anybody should be confirming by hand on a Tuesday at a customer
  site.

- **The instance serves its own agent (intake E4).** Every Bor server hosts its
  own signed package repositories and emits a single copy-paste script that
  trusts the CA, adds the repository, installs, enrolls and starts. Managed
  nodes never need internet. The same shape fits our station and Rust agent,
  and it is the honest answer to an air-gapped municipality.

- **Enrollment that does not need a token in an AD shop (intake D1).** The
  trigger for this one is narrower - a customer with Active Directory or
  FreeIPA - but it is worth naming here because it lands in the same code. A
  domain-joined machine can authenticate its own enrollment, which is the
  difference between a wizard per device and enrollment that simply happens.

## 2.0 - when the secrets model changes

**Trigger: the first secret we are required to rotate across a live fleet.**
Everything below either changes a contract or reopens an ADR, which is what
makes it a major version rather than a feature. None of it should be attempted
piecemeal.

- **Secrets become generators, not files (intake B1, B3, B4, B5, B7).** This is
  the largest single idea in the three projects we read. clan does not store
  secrets; it stores their *derivation*. A generator declares what files it
  produces, what it needs prompted, and the script that produces them - and one
  command creates, encrypts and distributes whatever is missing. A new device
  needs no "remember to add the recipient" step because there is no manual step
  to forget, and rotation becomes declarative: change an input, the hash
  changes, the secret regenerates. Fleet-wide secrets are generated once and
  reused; generators can depend on each other, so a CA feeds a certificate
  rather than a human doing it twice.

  We solved the recipient half already - a newly imaged device's host key is
  registered automatically. This is the other half, and it replaces a model
  rather than extending it, which is why it waits for a major version.

- **Secrets bound to a physical token (intake B11, I6).** The surprise in
  Sécurix: their WireGuard private key is age-encrypted to an identity living
  in a YubiKey PIV slot, so absent the token the ciphertext is inert. Not
  "protected by a passphrase", not "readable by root" - unusable. For an admin
  device holding fleet credentials that is the right guarantee, and it composes
  with the generator model above as a choice of recipient. The four token
  *uses* land at 1.2; this one waits for the model it plugs into.

- **Backup and restore as a first-class abstraction (intake G5).** We have no
  backup story at all, and for a municipal fleet that is a question we will be
  asked rather than one we get to choose. clan's shape is the right one: state
  directories declare their folders plus pre-backup and post-restore hooks, so
  a service can dump a database or stop itself, and providers implement
  `list`/`create`/`restore` against that interface. It is a new capability with
  its own tenancy and retention questions, so it is a 2.0 conversation.

- **Break-glass reachability, if the argument is won (intake F2).** Peer-to-peer
  SSH that is off by default, armed per device the way the wipe intent is
  armed, expiring, and audited. It is the *only* version of clan's reachability
  work that could survive our threat model, and it still requires reopening
  **ADR 0023** - not the threat model, which describes risks rather than making
  choices. clan's own documentation warns that enabling their service exposes
  the SSH daemon to anyone on that network. If the ADR's conditions are not
  met, this becomes a line under "deliberately not doing" and stops being
  asked.

- **A device-local upgrade path, if it turns out not to be a channel (intake
  C8).** Pilot users cannot update anything themselves today. Sécurix ships an
  `upgrade` command usable by a device-local operator group, with a man page.
  Whether that violates pull-only is a genuine question rather than a
  formality: it is initiated *on* the device, by a local human, and it still
  pulls. ADR 0023 records that this is probably *not* a violation and is the
  one of the four conflicts worth arguing. The argument gets settled there,
  before the work rather than during it.

## Unscheduled, and honest about why

These matter and none of them has a trigger yet. They move up the moment one
appears.

- **Push, for a fleet shape we do not have.** Devices pull, and **ADR 0023**
  now records why and - the half that was missing until 2026-08-10 - what
  would change it. Two triggers, both Bram's: **a server estate**, or **a
  second operating system**. Servers invert every property that made pull
  right; a non-NixOS target has nothing to converge with, so "pull" would stop
  describing a design and start describing an absence. A mixed answer is
  allowed: push for servers alongside pull for workstations. What is not
  allowed is acquiring a channel by accident, for one feature, without the
  argument. The reasoning is in the ADR; only the trigger belongs here.

- **Tenant isolation for the gate.** A cold edit blocks for one evaluation.
  On a single console that is honest - the operator asked for something new.
  With several tenants on one gate slot it is not, because one organisation's
  cold edit becomes everybody's wait. This belongs with the multi-tenancy
  design, not with the write path. Measured and reasoned in
  `architecture/scale.md`.
- **Parallel evaluation workers, and profiling the 3 GiB.** Each worker needs
  roughly 3 GiB, so this waits on moving the gate off-cluster. And nobody has
  looked at where that 3 GiB actually goes - it is high for a NixOS toplevel.
- **Sovereign flake mirrors and our own cache as the fleet substituter**
  (ADR 0016). The fork exists as phase one.
- **OpenBao as the cell-secret backend**, plus HA, auto-unseal and TLS. Note
  the ordering trap recorded in the OpenBao task: an admin recovery path must
  not run through a secret that only the thing being recovered can decrypt.
- **SCIM inbound, and LDAP direct-bind as a console auth source.**
- **comin records a failed switch as successful (#55).** The control plane no
  longer believes it, so this is about the story an engineer reads while
  standing in front of a broken machine. Establish first whether it is upstream
  behaviour.
- **A working auditd ruleset in the core (intake C6).** DAWO-NixOS defers
  auditd because it is a no-op on nixpkgs 26.05 - an `auditctl` module bug that
  breaks every `nixos-rebuild` if real rules are enabled - and the block warns
  rather than pretending to cover. The trigger is the upstream fix, which is
  not ours to schedule. Sécurix has a working `auditd.conf` and ruleset ready
  to port the day it lands.
- **Tamper-restore for files outside the Nix store (intake C9).** On NixOS a
  rebuild is the restore, so this matters only for the mutable state the
  station and agent own. Bor's approach is worth remembering when it does:
  watch the parent directories so atomic renames are seen, and suppress your
  own writes so the watcher does not fight itself.
- **A crypto-compliance reference with a deployment checklist (intake A6).**
  Every algorithm mapped to FIPS 140-3, BSI TR-02102, ANSSI RGS, ETSI EN 319
  412 and NIS2. The trigger is the first customer security questionnaire that
  asks, and Bor's `SECURITY.md` shows how thick the answer has to be. Related
  and separately unscheduled: FIPS-validated builds (intake A7) and a PKCS#11
  HSM for the cell CA key (intake B9), both of which wait for a customer who
  actually requires them rather than one who is impressed by them.
- **Central journal shipping, and per-device metrics (intake G6, G7).**
  Sécurix ships journald to a remote sink and exports node metrics including
  power draw. Both overlap what Wazuh should be doing for us, so this waits on
  the Wazuh work in 1.1 concluding what is left over. The power-consumption
  reporting is the interesting half - it is a public-sector reporting line, not
  an observability nicety.
- **A container test driver alongside VM tests (intake H4).** clan runs both.
  Faster device-level tests would be welcome; nothing currently forces it.

## What we are deliberately not doing

Kept here so the question stops coming back.

- **Forge drivers.** Provisioning is manual by decision. A driver interface
  returns only if provisioning volume ever demands it.
- **A remote command channel.** Devices pull; the console never pushes. Every
  remote capability is an *intent* the device chooses to act on. This is not a
  limitation to be engineered away - it is the reason there is no channel for
  an attacker to abuse either.

  Two specific temptations were read in full and refused (intake F1, E5). clan
  models transport as a priority ladder - peer-to-peer SSH, direct, WireGuard,
  ZeroTier, Mycelium, Tor - and falls back automatically until one connects. It
  is elegant, and it exists because clan *must* reach a machine in order to
  deploy at all. We must not. Likewise their installer announces itself as a
  Tor onion service, which genuinely solves imaging a machine behind NAT with
  no known address; it is still a listening remote channel, and the answer to
  that problem belongs in the provisioning station on the local network.
- **Our own console MFA (intake D5).** Bor ships TOTP and WebAuthn built in.
  Console authentication is OIDC by design and the identity provider owns MFA;
  a second authenticator is a second thing to get wrong, and it would sit
  outside the customer's own account lifecycle.
- **An image per user (intake, Sécurix `mkTerminals`).** Sécurix builds a
  separate signed image per agent, with that person's identity, VPN profiles
  and key handles baked in. It is coherent and it is the opposite of our model:
  one image, and the console composes the person onto it. Recorded because the
  approach is defensible and someone will propose it.
- **A crippled edition.** Everything is in this repository under the EUPL.
  What BB Open sells is work, not permission.

## Keeping this honest

Two habits, both learned the expensive way:

**Measure before building.** The asynchronous validation queue was in this
roadmap until it was timed: an ordinary edit holds the write lock for fifteen
milliseconds, so the queue would have optimised a path that is already fast and
changed what "saved" means to an operator. It was removed on the strength of a
measurement rather than an opinion.

**A finding without a trigger is a note, not a plan.** Everything above either
names what forces it or sits under "unscheduled" and says so.
