package observed

import "testing"

// Metrics feeds policy conditions (ADR 0017). The property that carries the
// most weight is not the arithmetic but the absence: a figure the device never
// reported must not appear at all, because a condition on an absent metric is
// unknown, while a 0 would read as a definite - and alarming - measurement.

func TestMetricsComputesFreeNotUsed(t *testing.T) {
	m := Usage{CPUPct: 40, MemUsedMB: 6144, MemTotalMB: 8192, DiskUsedGB: 400, DiskTotalGB: 500}.Metrics()
	for name, want := range map[string]float64{
		"cpu.used_percent":    40,
		"memory.total_mb":     8192,
		"memory.free_percent": 25,
		"disk.total_gb":       500,
		"disk.free_gb":        100,
		"disk.free_percent":   20,
	} {
		if got, ok := m[name]; !ok || got != want {
			t.Errorf("%s = %v (present %v), want %v", name, got, ok, want)
		}
	}
}

// An older agent reports nothing. Every metric must be absent, so that a
// policy requiring 15% free disk stays silent about a device it cannot see
// rather than accusing it of having a full disk.
func TestUnreportedUsageYieldsNoMetrics(t *testing.T) {
	if m := (Usage{}).Metrics(); len(m) != 0 {
		t.Fatalf("a device that reported nothing produced metrics %v; absent must stay absent", m)
	}
}

// The partial case is the one that would slip through: an agent that reports
// CPU but has no disk probe. Disk must be absent, not zero.
func TestPartialReportOmitsOnlyTheMissingMetrics(t *testing.T) {
	m := Usage{CPUPct: 40}.Metrics()
	if _, ok := m["cpu.used_percent"]; !ok {
		t.Error("cpu was reported but is missing from the metrics")
	}
	for _, name := range []string{"disk.free_percent", "disk.free_gb", "disk.total_gb", "memory.free_percent"} {
		if v, ok := m[name]; ok {
			t.Errorf("%s = %v for a device that never probed it; a policy would judge it as if measured", name, v)
		}
	}
}

// A full disk is a real measurement and must be present as 0, which is exactly
// the value the absent case must never produce.
func TestFullDiskIsZeroFreeNotAbsent(t *testing.T) {
	m := Usage{DiskUsedGB: 500, DiskTotalGB: 500}.Metrics()
	if v, ok := m["disk.free_percent"]; !ok || v != 0 {
		t.Fatalf("disk.free_percent = %v (present %v), want a present 0 - a full disk is measured, not unknown", v, ok)
	}
}
