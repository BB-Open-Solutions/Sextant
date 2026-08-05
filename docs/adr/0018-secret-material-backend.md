# 0018 - How secret material reaches a device

## Status

Accepted 2026-08-05 (Bram): "we hebben geen haast laten we de juiste
keuze nu maken". Supersedes the backend half of design 0007, whose
"Status: No code yet" line is stale - see *Measured state* below.

This ADR settles the MATERIAL path only. The split that the console
registers a NAME and never the value (`fleet.json` secretRefs, audited
commit) is not in question here and does not change.

**This document was rewritten the same day it was written, and the first
version is worth knowing about.** It decided for sops-nix on the premise
that the OpenBao path did not exist yet - taken from design 0007's own
status line, without checking the overlay. The module was already built
and running. The corrected comparison reverses the decision. Recorded
rather than quietly replaced, because the failure was reading a document
where the code was the answer, and this repository spent 2026-08-05
removing exactly that class of mistake from its own docs.

## Context

The material path is agenix: age-encrypted files in the overlay
repository, decrypted on the device by its own SSH host key at
activation, resolving to `/run/agenix/<name>`. Recipients are the host
keys of every enrolled device plus an admin identity a person holds.
`scripts/rekey-secrets.sh` re-encrypts when the recipient set changes.

That arrangement has one large virtue and four defects, and the virtue
is easy to undervalue because nothing draws attention to it.

**The virtue: a device needs nobody.** It decrypts its own secrets with
a key that never leaves it. Console down, cluster down, mesh down - the
machine still activates a generation with working integrations. Every
other part of this product rests on the same property: devices pull,
nothing is pushed, and the control plane is never in the path of a
device staying correct.

**The defects, in the order they hurt:**

1. **Custody.** The admin identity is a file a human keeps
   (`~/.ssh/bbuijs`). Lose it and the devices, and the secrets are not
   hard to recover - they are gone. This nearly happened on 2026-07-31,
   when no admin identity existed at all and the de facto key was one
   device's host key; a single reinstall of that machine would have made
   the fleet unopenable. Task #34.
2. **No read audit.** Git shows who committed a re-encryption. Nothing
   shows which device read which secret, or when.
3. **Revocation does not revoke.** Removing a recipient does not retract
   ciphertext already published, and git history keeps it permanently.
4. **Rekeying is toil**, done by hand with a key one person holds.

Sovereignty rules out the cloud KMS options, and "does it integrate with
a vault" is a real tender question even though it is not a technical
one.

## Measured state (2026-08-05)

Design 0007 says "No code yet". The overlay disagrees:
`dawo.openbao.*` in `bb-open/modules/integrations.nix` is complete and
deployed.

- A oneshot fetches each named secret over the vault's HTTP API on boot,
  and a timer repeats at `OnBootSec=2min`, `OnUnitActiveSec=1h`, so a
  rotation in the vault reaches devices with no configuration change.
- Secrets land in a `RuntimeDirectory` (tmpfs, `0700`), each file
  `0400`.
- The device authenticates with a token that is itself an agenix
  secret (`tokenSecret`). agenix bootstraps the vault; the vault serves
  the rest. That layering is sound and stays.
- A symlink from `/run/agenix/<name>` keeps the secret-ref contract
  backend-agnostic, so integrations resolve one path either way.

So option C below is not a proposal. It is running, and what remains of
it is a mesh route and an end-to-end proof.

**It also carries a defect this ADR fixes.** The symlink is conditional:

```sh
# agenix owns /run/agenix when present; only fill the gap.
if [ ! -e "/run/agenix/${name}" ]; then
```

A secret present in both backends is served by agenix, silently. Rotate
it in the vault and the device keeps the old value while every layer
reports success. Precedence exists, is undocumented, and points the
wrong way for the one operation the vault was added to provide.

## Options

**A. Keep agenix alone.** Zero work, keeps the virtue, keeps all four
defects, and cannot answer the vault question at all.

**B. agenix + agenix-rekey.** Automatic re-encryption per host, master
identity on a YubiKey, FIDO2 key or TPM. Fixes defects 1 and 4 - the
acute ones - cheaply and with no runtime dependency. Fixes neither 2 nor
3. A third-party flake with its own bus factor.

**C. Finish the OpenBao path that exists.** Per secret, exactly one
backend: agenix for anything a machine needs to reach a working state
alone, the vault for anything that must be rotatable, auditable and
revocable. Remaining work: the mesh route, an e2e proof, and the
precedence fix.

**D. Replace agenix with sops-nix**, one file encrypted to several key
sources (host age key derived via `ssh-to-age`, OpenBao transit).
Elegant: one ciphertext, several ways to open it, so no two stores can
disagree. But it replaces the secret mechanism in the DAWO core -
upstream, fork and merge request - to arrive at the same per-secret
choice C already offers, and it does **not** deliver rotation without a
git commit, because transit wraps the data key while the value stays in
the file.

## Decision

**Option C.** Finish the path that is built.

The reasoning that chose D still holds and now points here: prefer the
fewest moving parts. D builds something new alongside something working;
C completes it. D's one genuine advantage - a single mechanism - is
bought by replacing a component of the upstream core, and its headline
benefit over agenix is a benefit C already has.

**Every secret names exactly one backend.** Not a primary with a
fallback: two stores holding the same value can disagree, and a device
serving a stale secret while reporting success is the failure class this
codebase spent the day removing. The choice is per secret:

- **Autonomous (agenix).** The device opens it with no network. For
  anything needed to reach a working state: mesh enrolment, endpoint
  agent registration, directory bind, and the vault token itself.
- **Managed (OpenBao).** Rotatable without a commit, every read audited,
  revocable by disabling the token or policy. For anything where those
  three are worth more than surviving an outage.

**A name configured in both is a configuration error**, refused at the
gate rather than resolved by precedence. Silent precedence is what made
this worth an ADR.

**The master identity moves to hardware** (YubiKey, FIDO2 or TPM). Task
#34 is not a documentation problem, and a procedure in a handbook is not
a control.

## Consequences

- **Two mechanisms remain**, deliberately. The cost is that operators
  must know which secret is which; the fit-gap and the handbook must say
  so, and the console should show it per secret rather than leaving it
  to lore.
- **The vault is load-bearing only for Managed secrets.** Availability
  scales with that list rather than being all-or-nothing, which is why
  this ADR does not require HA or auto-unseal for 1.0. A fleet whose
  secrets are all Autonomous keeps working with the vault down.
- **A Managed secret whose vault is unreachable must fail loudly.**
  Today the fetch unit is `set -eu` in a loop, so the first failure
  silently skips every later secret and the timer retries in an hour.
  The device must report which backend served each secret, and a Managed
  secret that could not be fetched belongs on the incident board.
- **agenix stays on the critical path** as the bootstrap for the vault
  token. That is correct - something has to open the first door - and it
  means defects 1 and 4 do not disappear. Hardware custody addresses 1;
  4 shrinks to the Autonomous set.
- **Option D remains the answer if agenix goes unmaintained.** Its last
  release is some months old, which is unremarkable for a small finished
  tool and would become material if the ecosystem moved. This ADR is the
  place to revisit.

## Before this is settled

1. **The mesh route** (the same routing-peer decision as LDAP and
   Wazuh), then one secret fetched end to end on real hardware.
2. **The precedence fix**, with a test that a name in both backends is
   refused rather than resolved.
3. **Reporting**, so a Managed secret served from the wrong place, or
   not at all, is visible instead of inferred.

Until 1 and 2 land, the interim stands: the existing admin identity gets
its hardware token and its break-glass copy, and the recovery is walked
once - a path nobody has followed is an assumption, not a control.

## References

- design 0007 (secret backends) - status line stale, superseded here
- `bb-open/modules/integrations.nix` - the `dawo.openbao.*` module
- ADR 0009 (cells), ADR 0016 (sovereign flake chain)
- `docs/handbook/src/operators/secrets.md` - operator guidance
- `docs/e2e-2-findings.md` - the 2026-07-31 near-miss on custody
