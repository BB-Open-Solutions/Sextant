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

## Decision: posture is an image-time property (2026-07-28)

Found live during the first inspoelronde: a device imaged while its
resolved config did NOT target Secure Boot / TPM2 completes its job
with those steps skipped, and enabling the keys afterwards does not
restart the ceremony - deliberately. The material the ceremony consumes
(the staged sbctl platform keys, the one-shot LUKS enrol key) exists
only during install and is shredded after use; there is no safe
config-only path to enrol later.

The rule, decided with the operator: **changing secureboot.enable /
diskUnlock.tpm2.* on an already-enrolled device requires re-imaging.**
Set the posture keys on the group BEFORE dispatching the image. The
console must make this legible: a posture-key change that reaches
devices with a completed image job should say "takes effect at the
next re-image" instead of implying a live ceremony (UI-audit item; the
device posture panel's step texts assume mid-ceremony state today).

Refinement (same day): fixed does not mean everything is fixed.

- `secureboot.enable` is GROUP-scope only and image-time; the settings
  editor hides it at device scope. The one device-level exception stays
  the posture panel's guarded temporary-off-for-reinstall path.
- TPM2 splits into capability vs use. The keyslot ENROLMENT is the
  image-time capability: the ceremony seals whenever Secure Boot is
  targeted and a TPM2 is present, regardless of the unlock toggle.
  `diskUnlock.tpm2.enable` then becomes a pure runtime toggle - "use
  the enrolled slot for auto-unlock, yes/no" - safely flippable via
  config because the slot already exists. Requires the ceremony change
  (always-seal) plus catalog wording; not built yet.

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
