package observed

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestValidate(t *testing.T) {
	ok := CheckIn{Tag: "lt-1", Revision: "v1", Phase: Running}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// Phase optional (heartbeat).
	if err := (CheckIn{Tag: "lt-1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	bad := []CheckIn{
		{},
		{Tag: strings.Repeat("a", 64)},
		{Tag: "x", Phase: "flying"},
		{Tag: "x", Revision: strings.Repeat("r", 129)},
		{Tag: "x", Error: strings.Repeat("e", 4097)},
	}
	for i, c := range bad {
		if err := c.Validate(); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
}

func TestOnline(t *testing.T) {
	s := DeviceStatus{LastSeen: t0}
	if !s.Online(t0.Add(2 * time.Minute)) {
		t.Error("2m ago should be online")
	}
	if s.Online(t0.Add(4 * time.Minute)) {
		t.Error("4m ago should be offline")
	}
	if (DeviceStatus{}).Online(t0) {
		t.Error("never-seen should be offline")
	}
}

func TestHealthy(t *testing.T) {
	base := DeviceStatus{Revision: "v2", Phase: Running, LastSeen: t0}
	now := t0.Add(time.Minute)
	if !base.Healthy("v2", now) {
		t.Error("healthy device judged unhealthy")
	}
	cases := []struct {
		name string
		mod  func(DeviceStatus) DeviceStatus
	}{
		{"wrong revision", func(s DeviceStatus) DeviceStatus { s.Revision = "v1"; return s }},
		{"offline", func(s DeviceStatus) DeviceStatus { s.LastSeen = t0.Add(-time.Hour); return s }},
		{"error reported", func(s DeviceStatus) DeviceStatus { s.Error = "unit failed"; return s }},
		{"still installing", func(s DeviceStatus) DeviceStatus { s.Phase = Installing; return s }},
	}
	for _, tc := range cases {
		if tc.mod(base).Healthy("v2", now) {
			t.Errorf("%s judged healthy", tc.name)
		}
	}
	// Empty phase (pure heartbeat devices) counts as running.
	noPhase := base
	noPhase.Phase = ""
	if !noPhase.Healthy("v2", now) {
		t.Error("empty phase should be healthy")
	}
}

func TestUsageValidationAndReported(t *testing.T) {
	if (Usage{}).Reported() {
		t.Error("zero usage must read as not reported")
	}
	if !(Usage{MemTotalMB: 8192}).Reported() {
		t.Error("a usage with total memory must read as reported")
	}
	good := CheckIn{Tag: "lt-1", Usage: Usage{CPUPct: 40, MemUsedMB: 4096, MemTotalMB: 8192, DiskUsedGB: 100, DiskTotalGB: 512}}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid usage rejected: %v", err)
	}
	for name, u := range map[string]Usage{
		"cpu>100":         {CPUPct: 101},
		"cpu<0":           {CPUPct: -1},
		"neg mem":         {MemUsedMB: -1},
		"mem used>total":  {MemUsedMB: 100, MemTotalMB: 50},
		"disk used>total": {DiskUsedGB: 100, DiskTotalGB: 50},
	} {
		if err := (CheckIn{Tag: "lt-1", Usage: u}).Validate(); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}
