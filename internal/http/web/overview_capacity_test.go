package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

func dev(hw string, cores, ram, disk int) fleet.Device {
	return fleet.Device{Hardware: hw, Spec: &fleet.HardwareSpec{Cores: cores, MemGB: ram, DiskGB: disk}}
}

func TestFleetCapacityAggregates(t *testing.T) {
	f := &fleet.Fleet{Devices: map[string]fleet.Device{
		"a": dev("lenovo-t495s", 8, 32, 512),
		"b": dev("lenovo-t495s", 8, 32, 512),
		"c": dev("intel-nuc", 4, 16, 256),
		"d": {Hardware: "no-spec"}, // no spec: skipped
		"e": {Hardware: "old", State: fleet.DeviceRetired, // retired: skipped
			Spec: &fleet.HardwareSpec{Cores: 99, MemGB: 99, DiskGB: 99}},
	}}
	cap := fleetCapacity(f)

	if cap.Devices != 3 {
		t.Fatalf("active devices with spec = %d, want 3", cap.Devices)
	}
	if cap.Cores != 20 || cap.RAMGB != 80 || cap.DiskGB != 1280 {
		t.Fatalf("totals wrong: cores=%d ram=%d disk=%d", cap.Cores, cap.RAMGB, cap.DiskGB)
	}
	// Retired device's 99s must not leak in.
	if cap.Cores == 119 {
		t.Fatal("retired device counted in capacity")
	}
	// Cores donut: lenovo (16) before intel (4), lengths sum to 100%, offsets cumulative.
	segs := cap.CoreSeg
	if len(segs) != 2 || segs[0].Label != "lenovo-t495s" || segs[0].Value != 16 {
		t.Fatalf("core segments wrong: %+v", segs)
	}
	if segs[0].Offset != 0 || segs[1].Offset != -segs[0].Dash {
		t.Fatalf("segment offsets not cumulative: %+v", segs)
	}
	if segs[0].Dash+segs[1].Dash > 100 {
		t.Fatalf("segment lengths exceed the ring: %+v", segs)
	}
}

func TestFleetCapacityEmpty(t *testing.T) {
	if c := fleetCapacity(nil); c.Devices != 0 || c.CoreSeg != nil {
		t.Fatal("nil fleet must yield an empty capacity")
	}
	if c := fleetCapacity(&fleet.Fleet{}); c.Cores != 0 || len(c.CoreSeg) != 0 {
		t.Fatal("empty fleet must yield zero capacity")
	}
}

func TestFleetCapacityFoldsTail(t *testing.T) {
	// More than five profiles: the smallest fold into a single "other" slice.
	devs := map[string]fleet.Device{}
	for i, hw := range []string{"p1", "p2", "p3", "p4", "p5", "p6", "p7"} {
		devs[hw] = dev(hw, i+1, 0, 0) // cores 1..7
	}
	cap := fleetCapacity(&fleet.Fleet{Devices: devs})
	if len(cap.CoreSeg) != 6 { // top 5 + other
		t.Fatalf("expected 6 slices (5 + other), got %d", len(cap.CoreSeg))
	}
	if cap.CoreSeg[5].Label != "other" {
		t.Fatalf("last slice must be 'other', got %q", cap.CoreSeg[5].Label)
	}
}
