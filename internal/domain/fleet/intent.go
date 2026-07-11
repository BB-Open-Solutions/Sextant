package fleet

import "fmt"

// intent.go: remote-action intents as config-as-data (design 0004). An
// intent is a field on the device record, so arming, delivery and
// clearing are all audited git commits - no imperative device control.

// SetDeviceIntent arms a remote action on a device. Wipe is guarded:
// destroying a device's data must be a deliberate two-step act, so the
// device has to be locked first unless force is set (a lost, never-locked
// device). Retired devices cannot be targeted - they have no live agent.
func SetDeviceIntent(tag, intent string, force bool) Mutation {
	return func(f *Fleet) error {
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		if d.Retired() {
			return fmt.Errorf("device %q is retired; no agent to act on an intent", tag)
		}
		switch intent {
		case IntentLock:
			// idempotent-safe: re-arming lock is fine.
		case IntentWipe:
			if d.Intent != IntentLock && !force {
				return fmt.Errorf("wipe requires the device to be locked first (or force)")
			}
		default:
			return fmt.Errorf("unknown intent %q (lock|wipe)", intent)
		}
		d.Intent = intent
		f.Devices[tag] = d
		return nil
	}
}

// ClearDeviceIntent cancels a pending action (e.g. a found laptop). A wipe
// that a device has already executed cannot be recalled, but clearing
// stops one that has not yet been delivered.
func ClearDeviceIntent(tag string) Mutation {
	return func(f *Fleet) error {
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		d.Intent = IntentNone
		f.Devices[tag] = d
		return nil
	}
}
