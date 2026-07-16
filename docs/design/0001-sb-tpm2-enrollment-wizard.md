# Design 0001: Secure Boot + TPM2 enrollment wizard

Status: built and superseded in part by wizard v2 (task #89, v0.62.x).
Key change vs. this design: the signing keys are **pre-generated on the
station and staged with the install** (`--extra-files`), so lanzaboote signs
the boot chain during the install itself - the separate "audit mode" deploy
round-trip below no longer exists. The remaining ceremony is device-driven:
a derived `provision` intent lets `sextant-actd` enrol the keys in firmware
setup mode and seal the LUKS keyslot to the TPM2 (using a staged one-shot
enrol key, shredded after use), with each milestone acked and verified via
posture. Which phases apply is gated by the device's resolved config
(`secureboot.enable`, `diskUnlock.tpm2.enable`) and hardware capability.

## Problem

Bringing a device to full security posture is a multi-step, order-
sensitive physical process a customer must not have to memorize:

1. Secure Boot OFF in firmware -> install the image
2. Boot -> lanzaboote in audit mode -> create + enroll keys (sbctl)
3. Reboot into firmware -> Secure Boot ON
4. TPM2: enroll LUKS auto-unlock bound to PCR7 (systemd-cryptenroll)
   - MUST happen after SB is on, or the PCR7 binding is to the wrong state

The console must show per device where it stands and what to do next.

## Architecture decision

**Detect, don't declare.** The device's real posture comes from the agent
(facts + a small posture probe), never from an operator checkbox. The
wizard renders the gap between observed posture and target posture as a
checklist with the next physical action highlighted.

### Data model

New observed-plane columns (migration 0004_posture.sql):

```sql
ALTER TABLE device_status ADD COLUMN sb_state text NOT NULL DEFAULT '';
ALTER TABLE device_status ADD COLUMN tpm2_state text NOT NULL DEFAULT '';
```

Values (domain/observed/posture.go):
- sb_state: "" (unknown) | off | audit | enforcing
- tpm2_state: "" | absent | present | enrolled

### Agent probe (agent/src/posture.rs)

Read-only, no privileges beyond existing:
- SB: read /sys/firmware/efi/efivars/SecureBoot-* (byte 4: 0/1) plus
  lanzaboote presence (/boot/EFI/Linux signed entries) -> off/audit/enforcing.
  Heuristic: SecureBoot=1 -> enforcing; SecureBoot=0 + sbctl keys present
  (/var/lib/sbctl exists) -> audit; else off.
- TPM2: /dev/tpmrm0 exists -> present; `systemd-cryptenroll <luks-dev>
  --list` mentions tpm2 (or /etc/crypttab tpm2 option) -> enrolled.
  No tpm device -> absent.
Fields ride the CheckIn JSON: `"sb":"audit","tpm2":"present"` (extend
observed.CheckIn with SB, TPM2 string fields; Validate() accepts the
enum or empty).

### Target posture (config-as-data)

Already exists: dawo.secureboot.enable and dawo.diskUnlock.tpm2.enable
resolve per device through the normal chain. Target = resolved values.

### Wizard rendering (console)

Device page gains a "Security posture" panel (internal/http/web/
device.html + pages.go):

| observed vs target | rendered step |
|---|---|
| target SB on, sb=off | 1. Install with SB off -> 2. NEXT: enable audit mode (set dawo.secureboot.enable at this device, deploy) |
| sb=audit | NEXT: reboot to firmware, switch Secure Boot ON |
| sb=enforcing, target tpm2 on, tpm2=present | NEXT: run `systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 <dev>` (one-liner shown, copy button) |
| tpm2=enrolled | posture complete (green) |
| tpm2=absent, target on | warning: no TPM2 present - hardware issue or firmware disabled |

Order enforcement in the UI text only (declarative model: the config is
already correct; the physical steps are the human's). The panel shows a
red warning when TPM2 enrollment is attempted while sb != enforcing
(PCR7 would bind to the wrong measurement).

## Files to touch

- agent/src/posture.rs (new) + main.rs wiring + tests (mock /sys paths
  via injectable root dir)
- internal/domain/observed/observed.go: CheckIn.SB/TPM2 + validation
- internal/adapters/postgres/migrations/0004_posture.sql + Upsert/Get
- internal/app/inventory.go: StatusView gains SB/TPM2
- internal/http/web/pages.go device(): posture panel data (resolved
  target via f.Resolve(tag)["secureboot.enable"] etc.)
- templates/device.html: posture panel
- OpenAPI: document new CheckIn fields (additive)

## Test plan

- agent: posture derivation table-driven over fake sysfs trees
- Go: CheckIn validation accepts/rejects enum; Upsert round-trips;
  posture panel renders each state (extend web tests)
- E2E on the t495s (SB-capable test device): walk the four states, watch
  the panel advance

## Acceptance

Operator sees per device a stepwise checklist that matches the physical
reality within one check-in interval, with exactly one highlighted next
action, and cannot be misled by stale/self-declared state.
