package fleet

import (
	"testing"
	"time"
)

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
		if err := AddDevice(tag, Device{Hardware: "t495s", Class: "laptop", Groups: []string{"laptops"}}, time.Now())(f); err != nil {
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
	}, time.Now())(f); err != nil {
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

// The reaper is the safety net, not the fix - a provisional device already
// counts toward nothing - so it may only take records that are genuinely
// abandoned, and it must never guess.
func TestAbandonedEnrolmentsOnlyTakesWhatItCanAgeAndProve(t *testing.T) {
	old := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 8, 3, 12, 0, 0, 0, time.UTC)
	cutoff := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

	f := &Fleet{Groups: map[string]Group{"laptops": {}}, Devices: map[string]Device{
		"abandoned":   {Hardware: "hw", State: DeviceProvisional, Enrolled: old},
		"still-going": {Hardware: "hw", State: DeviceProvisional, Enrolled: recent},
		// Predates the Enrolled field. Guessing its age would let a sweep
		// delete a device it knows nothing about.
		"unstamped": {Hardware: "hw", State: DeviceProvisional},
		// Reported long ago, so it is a real machine however old the record.
		"real":    {Hardware: "hw", Enrolled: old},
		"retired": {Hardware: "hw", State: DeviceRetired, Enrolled: old},
	}}

	got := f.AbandonedEnrolments(cutoff)
	if len(got) != 1 || got[0] != "abandoned" {
		t.Fatalf("reaped %v, want only [abandoned]", got)
	}
}

// Re-imaging the same chassis must update its unconfirmed enrolment rather
// than mint another. The match is deliberately narrow, and the cases it
// REFUSES matter more than the one it accepts.
func TestProvisionalBySerialMatchesOnlyWhatItCanBeSureOf(t *testing.T) {
	f := &Fleet{Devices: map[string]Device{
		"lt-first":  {Hardware: "hw", State: DeviceProvisional, ITAM: ITAM{Serial: "PF-1234"}},
		"lt-live":   {Hardware: "hw", ITAM: ITAM{Serial: "PF-9999"}},
		"lt-noserl": {Hardware: "hw", State: DeviceProvisional},
	}}

	if tag, ok := f.ProvisionalBySerial("PF-1234"); !ok || tag != "lt-first" {
		t.Fatalf("same chassis resolved to (%q,%v), want lt-first", tag, ok)
	}
	// Serial numbers are transcribed and reported by different tools; casing
	// and stray whitespace must not create a second record.
	if _, ok := f.ProvisionalBySerial("  pf-1234 "); !ok {
		t.Error("a differently-cased serial minted a second enrolment")
	}
	// A working machine being re-imaged is an operator's decision - whether it
	// keeps its tag, groups and settings is not inferable from a serial.
	if _, ok := f.ProvisionalBySerial("PF-9999"); ok {
		t.Error("an ACTIVE device was silently reused as if it were an abandoned enrolment")
	}
	// Specs are enrichment at enrolment, never guaranteed. Two unknowns are
	// not evidence of one machine.
	if _, ok := f.ProvisionalBySerial(""); ok {
		t.Error("an empty serial matched; every spec-less enrolment would collapse into one device")
	}
}

// A setting no device in scope can have is not dangerous - the generator skips
// it per device - but saving it silently would let an operator believe
// something happened.
func TestReachesNothingSpeaksUpOnlyWhenItCan(t *testing.T) {
	f := &Fleet{
		Groups: map[string]Group{"stations": {}, "laptops": {}, "leeg": {}},
		Devices: map[string]Device{
			"st-1": {Hardware: "hw", Class: "station", Groups: []string{"stations"}},
			"lt-1": {Hardware: "hw", Class: "laptop", Groups: []string{"laptops"}},
		},
	}
	workplace := CatalogEntry{Name: "apps.comms.enable", Classes: []string{"desktop", "laptop", "server"}}
	universal := CatalogEntry{Name: "ssh.enable"}

	if nothing, classes := f.ReachesNothing(workplace, "group:stations"); !nothing {
		t.Errorf("a workplace option on a station-only group reported as reaching something (classes %v)", classes)
	}
	if nothing, _ := f.ReachesNothing(workplace, "group:laptops"); nothing {
		t.Error("a workplace option on a laptop group was refused")
	}
	if nothing, _ := f.ReachesNothing(workplace, "org"); nothing {
		t.Error("a mixed org scope was refused; the laptops can have it")
	}
	if nothing, _ := f.ReachesNothing(universal, "group:stations"); nothing {
		t.Error("an untagged (universal) option was refused")
	}
	// An empty group is an ordinary place to put a setting before the devices
	// arrive - refusing there would be worse than saying nothing.
	if nothing, _ := f.ReachesNothing(workplace, "group:leeg"); nothing {
		t.Error("a setting on a group with no devices yet was refused")
	}
}
