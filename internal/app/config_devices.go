package app

import (
	"context"
	"fmt"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// config_devices.go: housekeeping on the device register.

// ReapAbandonedEnrolments removes provisional devices that were enrolled long
// ago and never reported. Returns the tags it removed.
//
// The safety net behind DeviceProvisional, not the fix. The fix is that a
// provisional device does not count toward a rollout, so an abandoned
// enrolment is already harmless; this only stops the register filling up with
// installations that never happened. That ordering matters: if this sweep
// never ran, nothing would break.
//
// One commit for the whole sweep rather than one per device, so a station
// that failed a dozen times produces a line an operator can read.
// now is passed rather than read: ConfigService owns no clock, and the caller
// (the rollout ticker) already has one that tests can move.
func (s *ConfigService) ReapAbandonedEnrolments(ctx context.Context, now time.Time, a ports.Author) ([]string, error) {
	cutoff := now.Add(-fleet.StaleProvisional)
	stale := s.Fleet().AbandonedEnrolments(cutoff)
	if len(stale) == 0 {
		return nil, nil
	}
	mut := func(f *fleet.Fleet) error {
		for _, tag := range stale {
			// Re-check inside the mutation: the fleet may have moved between
			// the read above and this write, and a device that reported in
			// the meantime must not be deleted out from under itself.
			d, ok := f.Devices[tag]
			if !ok || !d.Provisional() {
				continue
			}
			if err := fleet.RemoveDevice(tag)(f); err != nil {
				return err
			}
		}
		return nil
	}
	msg := fmt.Sprintf("devices: drop %d enrolment(s) that never reported (%s)",
		len(stale), strings.Join(stale, ", "))
	if err := s.Apply(ctx, mut, msg, a); err != nil {
		return nil, err
	}
	return stale, nil
}
