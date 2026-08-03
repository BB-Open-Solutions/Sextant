package fleet

import "testing"

// The incident these tests exist for: one laptop was enrolled four times by
// failed installs on 2026-07-31. All four records landed in ring bb-laptops,
// none had ever checked in, so the ring's present population was zero and no
// rollout could converge again. A handful of retries silently stopped the
// whole fleet from receiving updates.

func provisionalFleet(t *testing.T) *Fleet {
	t.Helper()
	f := &Fleet{
		Groups:  map[string]Group{"laptops": {}},
		Devices: map[string]Device{},
	}
	for _, tag := range []string{"lt-1", "lt-2"} {
		if err := AddDevice(tag, Device{Hardware: "t495s", Class: "laptop", Groups: []string{"laptops"}})(f); err != nil {
			t.Fatal(err)
		}
	}
	return f
}

// A device nobody has ever heard from must not be born counting.
func TestAddDeviceStartsProvisional(t *testing.T) {
	f := provisionalFleet(t)
	if got := f.Devices["lt-1"].State; got != DeviceProvisional {
		t.Fatalf("a freshly enrolled device is %q, want provisional", got)
	}
	if !f.Devices["lt-1"].Provisional() {
		t.Error("Provisional() disagrees with the stored state")
	}
	// An explicit state still wins, so importing a fleet that is already
	// running stays possible.
	if err := AddDevice("lt-9", Device{
		Hardware: "t495s", Class: "laptop", Groups: []string{"laptops"}, State: DeviceRetired,
	})(f); err != nil {
		t.Fatal(err)
	}
	if got := f.Devices["lt-9"].State; got != DeviceRetired {
		t.Errorf("an explicit state was overwritten: got %q", got)
	}
}

// The distinction that makes this safe: provisional devices still get built
// and configured. Leaving them out of the active set would hand a machine
// being installed an empty configuration.
func TestProvisionalStillBuildsButDoesNotCount(t *testing.T) {
	f := provisionalFleet(t)

	if got := len(f.ActiveGroupDevices("laptops")); got != 2 {
		t.Errorf("ActiveGroupDevices = %d, want both - a device being installed needs its config", got)
	}
	if got := len(f.ConvergingGroupDevices("laptops")); got != 0 {
		t.Errorf("ConvergingGroupDevices = %d, want 0 - neither device has ever reported", got)
	}
	// Settings and assignments must reach it too.
	if got := len(f.TargetDevices("group:laptops")); got != 2 {
		t.Errorf("TargetDevices = %d, want both", got)
	}
}

// This is the regression for the wedge itself. A ring whose every member is
// provisional has nothing that can converge, so the run must not sit on it -
// with the converging population at zero the ring is simply empty, and the
// engine already advances past an empty ring.
func TestARingOfOnlyProvisionalDevicesIsEmpty(t *testing.T) {
	f := provisionalFleet(t)
	if got := len(f.ReleasedGroupDevices("laptops")); got != 0 {
		t.Fatalf("a wave released to %d device(s) that have never reported", got)
	}
}

func TestFirstReportPromotesAndOnlyFromProvisional(t *testing.T) {
	f := provisionalFleet(t)

	if err := ActivateProvisional("lt-1")(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].State != DeviceActive {
		t.Fatalf("a device that reported is %q, want active", f.Devices["lt-1"].State)
	}
	if got := len(f.ConvergingGroupDevices("laptops")); got != 1 {
		t.Errorf("converging population is %d, want 1 after the first report", got)
	}

	// Called again on every subsequent beat: it must refuse rather than churn.
	if err := ActivateProvisional("lt-1")(f); err == nil {
		t.Error("promoting an already-active device was accepted; every heartbeat would commit")
	}
	// And a retired device that keeps beating must not quietly return.
	f.Devices["lt-2"] = Device{Hardware: "t495s", Groups: []string{"laptops"}, State: DeviceRetired}
	if err := ActivateProvisional("lt-2")(f); err == nil {
		t.Error("a retired device was reactivated by a check-in")
	}
}
