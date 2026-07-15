package app

import (
	"reflect"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// hostsFleet: a three-level group tree (hq > hq-desk > hq-desk-a) plus a
// sibling "other", with active and retired devices spread across it, so the
// blast-radius math is exercised through real ancestry.
func hostsFleet() *fleet.Fleet {
	return &fleet.Fleet{
		Version: 3,
		Groups: map[string]fleet.Group{
			"hq":        {},
			"hq-desk":   {Parent: "hq"},
			"hq-desk-a": {Parent: "hq-desk"},
			"other":     {},
		},
		Devices: map[string]fleet.Device{
			"d-leaf":    {Groups: []string{"hq-desk-a"}, Hardware: "hw"},
			"d-mid":     {Groups: []string{"hq-desk"}, Hardware: "hw"},
			"d-other":   {Groups: []string{"other"}, Hardware: "hw"},
			"d-retired": {Groups: []string{"hq-desk-a"}, Hardware: "hw", State: fleet.DeviceRetired},
		},
	}
}

func TestAffectedHosts(t *testing.T) {
	f := hostsFleet()
	cases := []struct {
		name string
		ref  string
		want []string
	}{
		// A device change gates exactly that host.
		{"device active", "device:d-leaf", []string{"d-leaf"}},
		// A retired device has no generator host attribute; gating it by name
		// would fail on a missing attr, so it falls back to org-wide (nil).
		{"device retired", "device:d-retired", nil},
		// A group change gates its whole active subtree, sorted, retired
		// devices excluded: hq covers both hq-desk and hq-desk-a members.
		{"group root", "group:hq", []string{"d-leaf", "d-mid"}},
		// A deeper group narrows the blast radius to its own members only.
		{"group leaf", "group:hq-desk-a", []string{"d-leaf"}},
		// A sibling subtree is disjoint.
		{"group sibling", "group:other", []string{"d-other"}},
		// An org-scoped or unknown-shaped ref has an unbounded blast radius, so
		// it validates everything (nil = gate the whole fleet).
		{"org wide", "org", nil},
		{"empty ref", "", nil},
		{"unknown prefix", "policy:x", nil},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AffectedHosts(f, c.ref)
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("AffectedHosts(%q) = %v, want %v", c.ref, got, c.want)
			}
		})
	}
}

// A group with only retired members must NOT collapse to a nil (org-wide) gate:
// it genuinely affects zero build-able hosts, so the gate should skip, not
// widen to the whole fleet.
func TestAffectedHostsRetiredOnlyGroupIsEmptyNotOrgWide(t *testing.T) {
	f := &fleet.Fleet{
		Version: 3,
		Groups:  map[string]fleet.Group{"dead": {}},
		Devices: map[string]fleet.Device{
			"gone": {Groups: []string{"dead"}, State: fleet.DeviceRetired},
		},
	}
	got := AffectedHosts(f, "group:dead")
	if len(got) != 0 {
		t.Fatalf("want no affected hosts, got %v", got)
	}
}
