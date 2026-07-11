package fleet

import "testing"

func intentFleet() *Fleet {
	return &Fleet{
		Version: 3,
		Devices: map[string]Device{
			"lt-1": {Hardware: "hw"},
			"lt-2": {Hardware: "hw", State: DeviceRetired},
		},
	}
}

func TestSetDeviceIntent(t *testing.T) {
	f := intentFleet()

	// Lock arms directly.
	if err := SetDeviceIntent("lt-1", IntentLock, false)(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].Intent != IntentLock {
		t.Fatal("lock not armed")
	}

	// Wipe after lock is allowed.
	if err := SetDeviceIntent("lt-1", IntentWipe, false)(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].Intent != IntentWipe {
		t.Fatal("wipe not armed after lock")
	}

	// Clear, then wipe without lock is refused unless forced.
	_ = ClearDeviceIntent("lt-1")(f)
	if err := SetDeviceIntent("lt-1", IntentWipe, false)(f); err == nil {
		t.Fatal("wipe without lock accepted")
	}
	if err := SetDeviceIntent("lt-1", IntentWipe, true)(f); err != nil {
		t.Fatal("forced wipe refused")
	}

	// Guards.
	if err := SetDeviceIntent("lt-1", "explode", false)(f); err == nil {
		t.Fatal("bogus intent accepted")
	}
	if err := SetDeviceIntent("lt-2", IntentLock, false)(f); err == nil {
		t.Fatal("retired device armed")
	}
	if err := SetDeviceIntent("ghost", IntentLock, false)(f); err == nil {
		t.Fatal("unknown device armed")
	}
}

func TestClearDeviceIntent(t *testing.T) {
	f := intentFleet()
	_ = SetDeviceIntent("lt-1", IntentLock, false)(f)
	if err := ClearDeviceIntent("lt-1")(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].Intent != IntentNone {
		t.Fatal("intent not cleared")
	}
	if err := ClearDeviceIntent("ghost")(f); err == nil {
		t.Fatal("unknown device cleared")
	}
}
