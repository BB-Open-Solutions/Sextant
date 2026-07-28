# 0010 - Diagnostics on demand

Status: draft for review. Decided 1.0-blocking 2026-07-28
(`docs/1.0-fit-gap.md` 5b).

## Problem

A support ticket today means SSH to the device and fishing the right
log out of the journal - exactly the workflow the console exists to
replace, and impossible once devices sit behind NAT without a
standing management tunnel. Intune's "collect diagnostics" is the
idiom: operator clicks, device uploads a bounded bundle, console
serves it.

## Design

Reuses the intent machinery from design 0004 end to end; the only new
piece is an upload endpoint, because the check-in body cap (320KiB,
`internal/http/api/checkin.go`) is deliberately far too small for
logs.

**Intent.** New non-destructive intent `diagnostics` next to
lock/wipe/reboot/provision. Editor-role or up, confirm dialog, audited
like every intent. No signed nonce - it destroys nothing - but it
rides the same single-intent-per-device field and clears on ack.

**Device side.** `sextant-actd` (root) collects a bounded bundle:
current-boot journal tail, `sextant-agent` unit log, failed-units
list, `nixos-version`. Hard cap 4MiB after gzip - newest entries win,
truncation is marked inside the bundle. Written to the spool; the
unprivileged agent uploads it and reports ack `diagnostics` (or
`diagnostics-failed`) on the next check-in.

**Upload.** `POST /api/device/{tag}/diagnostics`, per-device
credential auth, 4MiB cap - the same shape and limits as the station
report endpoint (`internal/http/api/station.go`). One bundle per
intent; a re-request overwrites.

**Console.** Device page lists the bundle (timestamp, size, requesting
operator), download audited. Retention: deleted automatically after 14
days and on device retire; one bundle per device at rest, no archive.

## Privacy

Journals can contain personal data (usernames, document paths). Hence:
operator-triggered only (no periodic collection), audit on request AND
download, short retention, bundle stored sealed with the same
secretbox the device secrets use. The org-level kill switch follows
the integrations toggle pattern so a tenant can forbid the feature
outright.

## Explicitly not

- No live shell, no arbitrary-command channel - fixed collector set
  only, extended only by shipping a new agent.
- No streaming; one bounded bundle per request.
- No fleet-wide collection; per-device, per-ticket.

## Verification

Agent: collector respects the cap and marks truncation; one-shot
upload lifecycle. Server: auth, cap, seal, retention sweep, audit
rows. E2e on t495s: request from the console, download the bundle,
verify the journal tail is present and the file disappears after the
retention window (clock-forwarded in test).
