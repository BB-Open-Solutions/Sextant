package app

import (
	"context"
	"fmt"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// settings_guard.go: refuses setting transitions that can brick enrolled
// hardware (delivery-process §7, Bram 17 jul). The canonical case: turning
// secureboot.enable off for a device whose firmware currently ENFORCES
// Secure Boot swaps its signed bootloader for an unsigned one - the next
// boot is refused by the firmware and the machine is unbootable until
// someone visits the BIOS. Config must follow the firmware here, never
// lead it: first take the firmware out of enforcing (setup mode / off,
// visible in the device's posture on its next check-in), then flip the
// setting. The guard encodes exactly that order.

// keyGuardSecureBoot is the one key guarded today; the shape generalises
// if more brick-capable settings appear.
const keyGuardSecureBoot = "secureboot.enable"

// GuardBrickingSettings rejects a batch of setting changes at scope when it
// would turn Secure Boot off for any affected device whose last reported
// posture is "enforcing". Devices with no posture (never checked in) do not
// block: there is nothing known to brick. inv may be nil (no observed plane
// configured), which disables the guard rather than every save.
func GuardBrickingSettings(ctx context.Context, cfg *ConfigService, inv *InventoryService, scope string, changes []SettingChange) error {
	if cfg == nil || inv == nil {
		return nil
	}
	var turnsOff, clears bool
	for _, c := range changes {
		if c.Key != keyGuardSecureBoot {
			continue
		}
		if c.Clear {
			clears = true
		} else if c.RawValue == "false" {
			turnsOff = true
		}
	}
	if !turnsOff && !clears {
		return nil
	}

	f := cfg.Fleet()
	for _, tag := range AffectedHosts(f, scope) {
		res, ok := f.Resolve(tag)[keyGuardSecureBoot]
		effective, _ := res.Value.(bool)
		if !ok || !effective {
			continue // already off for this device: nothing to brick
		}
		// A clear only changes the outcome when THIS scope is the source of
		// the current "on"; clearing an overshadowed value is a no-op.
		if clears && !turnsOff && res.Source.Scope != scope {
			continue
		}
		st, found, err := inv.Status(ctx, tag)
		if err != nil || !found {
			continue // no posture known: nothing demonstrably enforcing
		}
		if st.SB == observed.SBEnforcing {
			return fmt.Errorf(
				"refusing to disable Secure Boot: device %s currently enforces it in firmware - "+
					"an unsigned bootloader would not boot. Take the firmware out of enforcing first "+
					"(setup mode, or Secure Boot off) and retry once its posture reflects that",
				tag)
		}
	}
	return nil
}
