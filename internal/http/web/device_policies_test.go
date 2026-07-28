package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

func TestDevicePolicyRows(t *testing.T) {
	profiles, err := fleet.ParseProfiles([]byte(`[{"name":"laptop","settings":{"x":true}}]`))
	if err != nil {
		t.Fatal(err)
	}
	src, _ := profiles.Get("laptop")
	f := &fleet.Fleet{
		Version: 3,
		Devices: map[string]fleet.Device{
			"lt-1":  {Class: "laptop", Hardware: "hw"},
			"srv-1": {Class: "server", Hardware: "hw"},
		},
		Filters: map[string]fleet.Filter{
			"laptops": {Rules: []fleet.FilterRule{{Attr: fleet.AttrClass, Op: fleet.OpEq, Value: "laptop"}}},
		},
		Policies: map[string]fleet.Policy{
			"laptop":   {Settings: map[string]any{"x": true}, Profile: src.Provenance()},
			"stale":    {Settings: map[string]any{"x": false}, Profile: "laptop@old", Enforced: []string{"x"}},
			"handmade": {Settings: map[string]any{"y": 1}},
		},
		Assignments: []fleet.Assignment{
			{Policy: "laptop", Target: "org", Filter: "laptops"},
			{Policy: "stale", Target: "org"},
			{Policy: "handmade", Target: "device:srv-1"},
		},
	}

	rows := devicePolicyRows(f, profiles, "lt-1")
	if len(rows) != 2 {
		t.Fatalf("lt-1 rows = %+v, want laptop + stale", rows)
	}
	if rows[0].ID != "laptop" || rows[0].State != "current" || rows[0].Filter != "laptops" {
		t.Fatalf("row 0 = %+v", rows[0])
	}
	if rows[1].ID != "stale" || rows[1].State != "reapply" || rows[1].Enforced != 1 {
		t.Fatalf("row 1 = %+v", rows[1])
	}

	// The server misses the class filter but gets its device-targeted,
	// hand-made policy (empty state).
	rows = devicePolicyRows(f, profiles, "srv-1")
	if len(rows) != 2 || rows[0].ID != "handmade" || rows[0].State != "" || rows[1].ID != "stale" {
		t.Fatalf("srv-1 rows = %+v", rows)
	}
}
