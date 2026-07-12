# Design 0004: remote actions - lock, cryptographic wipe

Status: intent surface + lock BUILT; the DESTRUCTIVE wipe execution
(LUKS erase in the root actd unit) is the only part deferred.

Built (server + console + agent):
- fleet.Device.Intent (lock|wipe) + SetDeviceIntent (wipe needs lock-
  first or force) / ClearDeviceIntent, all audited commits
- API POST/DELETE /devices/{tag}/intent (org Owner); check-in returns
  200 + {intent} synchronously (no store-and-forward, no replay); the
  agent echoes an ack, recorded on device_status.ack (sticky)
- console red-zone panel (lock/wipe with typed-tag confirmation, cancel,
  armed/delivered indicator); sxctl devices lock/wipe/unlock
- agent: on an intent, react() writes the persistent lock flag and
  spools the request under /run/sextant-intent for a root executor

Deferred to the handoff (the one destructive syscall):
- the root `sextant-actd` unit that reads the spool and runs
  `cryptsetup luksErase` for wipe, plus `loginctl lock-sessions` for
  lock. The agent build spools and acks a wipe but performs NO
  destruction; that is logged loudly. Wiring the actd unit in
  deploy/nixos/agent.nix (path unit, root, locked down) completes it.

## Problem

Lost/stolen laptop: the org must be able to lock it and, if needed,
render its data unrecoverable. Sextant is declarative and has NO device
RCE - that is a feature (docs/capabilities.md). Actions must fit the
model: **an action is data the device converges on or reacts to**, and
every action is an audited commit.

## Architecture

### Intent as data

fleet.json device record gains one field (domain/fleet/model.go):

```
"intent": "" | "lock" | "wipe"
```

Mutations `SetDeviceIntent(tag, intent)` / `ClearDeviceIntent(tag)`
(mutate.go pattern; validate enum; wipe REQUIRES the device to already
be lock'd or a `force` flag - two-step by default). API:
`POST /api/v1/devices/{tag}/intent {"intent":"lock"}` - **org Owner**,
not device editor: destructive reach. Console: red-zone panel on the
device page with a typed confirmation (type the tag to arm wipe).

### Transport: check-in response

Until now check-in returns 204. It becomes 200 with a body when an
intent is pending (api/checkin.go):

```json
{"intent": "lock", "nonce": "<uuid>", "issuedAt": "..."}
```

The agent acks by echoing the nonce in its next check-in
(`"ack":"<nonce>"`); the server records intent_acked_at. This gives the
operator "delivered" vs "pending" without any push channel - the 60s
beat is the delivery mechanism. (No new listener, no queue.)

### Device side (agent)

- **lock**: run `loginctl lock-sessions` + create
  /run/sextant/locked flag; also write /etc/sextant/locked (persistent)
  so a reboot re-locks via a tiny systemd unit in the nix module.
  Reversible: intent cleared -> agent removes the flags.
- **wipe** (cryptographic): destroy the LUKS keyslots -
  `systemd-cryptenroll --wipe-slot=all <dev>` equivalent via
  `cryptsetup luksErase` on the root LUKS device, then poweroff. Data is
  unrecoverable without the (now destroyed) keys; the machine needs
  reprovisioning. Agent refuses wipe unless the nonce is fresh (<15 min)
  - a replayed old response cannot wipe.
- Both paths need root: the agent today runs DynamicUser. Split: the
  agent stays unprivileged and writes the intent to
  /run/sextant-intent/<nonce>.json; a separate root oneshot
  (sextant-actd, part of the nix module, path-unit on that directory)
  validates + executes. Privilege boundary stays explicit and tiny.

### Retire stays what it is

Retire = administrative lifecycle (M3.3). Wipe on a retired device is
allowed (still has a credential? no - retire revokes it; so wipe must
be issued BEFORE retire; document the order: lock -> wipe -> retire).

## Files to touch

- domain/fleet: intent field + mutations + tests
- api: intent endpoint (Owner) + checkin response body + ack recording
  (observed plane: intent_acked_at on device_status, migration 0005)
- agent: response parsing, nonce freshness, intent file drop; tests
  with the tiny_server harness (200-with-body case)
- deploy/nixos/agent.nix: sextant-actd path unit (root, locked down:
  only the two verbs)
- console: red-zone panel + typed confirm; evidence export includes
  intent commits automatically (they are commits)
- OpenAPI + sxctl (`devices lock TAG`, `devices wipe TAG --force`)

## Test plan

- domain: enum/two-step validation
- api: intent requires Owner; checkin returns body only when pending;
  ack round-trip
- agent: lock flag creation; wipe refuses stale nonce; actd unit tested
  on the t495s only (destructive - manual, documented in the test log)

## Acceptance

Owner arms lock -> device locks within one beat and stays locked across
reboot. Owner arms wipe (typed confirm) -> device destroys LUKS keys and
powers off; a replayed response cannot wipe; every step is a commit.
