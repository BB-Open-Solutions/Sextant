# 0009 - Recovery-key escrow

Status: BUILT (2026-07-28). Decided 1.0-blocking the same day
(`docs/1.0-fit-gap.md` 5b); closes threat-model residual risk R7.

Build notes (deviations from the draft below):
- The console reveal path already existed (`/devices/{tag}/secret/{kind}/
  reveal`, Owner-gated, audited) - nothing to build there.
- Confirmation is an explicit `X-Recovery-Key-Stored` response header, not
  a bare 2xx: the device deletes its copy only on that header, so a
  storeless server can accept the beat without silently losing the key.
- Rotation is coupled to the ceremony's staged enrol key (shredded after
  use): a NEW recovery key requires re-staging it (re-image/re-install),
  not merely re-running the provision intent. Good enough at current
  scale; revisit if TPM swaps become routine.

## Problem

A TPM2-bound LUKS device (design 0001) has no usable second factor
when the TPM breaks, the board is swapped, or a firmware update moves
PCR7: the disk is cryptographically gone. Werkplekbeheer expects a
recovery path day 1 - BitLocker's recovery-key escrow is the idiom.

Half of this exists. Imaged devices already seal a recovery key into
the device-secret store (`internal/http/api/station.go` `sealLUKS`,
kind `secret.LUKS`, AES-256-GCM, plaintext never stored). The gaps:

1. Wizard-path devices (the `provision` intent, design 0004) enroll
   TPM2 but never create or escrow a recovery key.
2. There is no console path to retrieve an escrowed key
   (`MarkRevealed` exists in the store; no endpoint, no UI).
3. R7: when no secret store is configured, the imaging path leaves the
   key in plaintext in `image_jobs.message` (`docs/threat-model.md`).

## Design

**Device side (provision intent, `sextant-actd`).** After the TPM2
keyslot enrolls, the executor generates a recovery key
(`systemd-cryptenroll --recovery-key`, its own keyslot), writes it to
the spool exactly once, and the agent carries it on the next check-in
in a new one-shot ack payload field. Transport is the existing
per-device-credential HTTPS check-in - no new channel. The device
deletes its copy as soon as the server acks receipt; the key is never
logged on either side. Re-running provision rotates: new key, old
recovery keyslot removed.

**Server side.** The check-in handler seals the key into
`device_secrets` under the existing `LUKS` kind (same row the
imaging path uses - one place to look per device) and drops the
plaintext. No new table; no new kind unless review prefers separating
wizard vs imaging provenance.

**Console.** Device page gains "reveal recovery key": org-Owner only,
explicit confirm, shown once, `MarkRevealed` audit trail (who + when,
visible on the device page afterwards). Reveal does not rotate;
rotation is the provision intent, so a revealed key stays valid until
an operator deliberately re-runs the ceremony after use.

**R7 closes.** Sealing requires the store: the imaging path refuses
the job (actionable error) instead of falling back to plaintext in
`image_jobs.message`. The chart already ships the secretbox key, so
the store is always configured in any real deploy.

## Security notes

- The key transits once, over the same mutually-authenticated HTTPS
  the wipe nonce trusts; at rest only as AES-256-GCM ciphertext.
- Reveal is the sensitive operation: Owner-gated, audited, single
  display. Threat model gains a control row; R7 moves to closed.
- The store never handling plaintext stays true - sealing happens in
  the handler before storage, as `sealLUKS` does today.

## Verification

Agent: unit test the one-shot spool/ack lifecycle (report once, delete
on ack, survive restart between). Server: handler seals + discards;
refusal without store. E2e on t495s: provision, reveal the key in the
console, boot the device with it (TPM cleared), re-provision, confirm
the old key is dead.
