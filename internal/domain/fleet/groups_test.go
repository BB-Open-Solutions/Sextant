package fleet

import "testing"

func TestGroupTreeHelpers(t *testing.T) {
	const j = `{
	  "version": 3,
	  "groups": {
	    "root-a": {},
	    "root-b": {},
	    "kid":    {"parent": "root-a"},
	    "stray":  {"parent": "ghost"}
	  },
	  "devices": {
	    "d1": {"groups": ["kid"], "hardware": "hw"},
	    "d2": {"groups": ["kid", "root-b"], "hardware": "hw"},
	    "d3": {"groups": [], "hardware": "hw"}
	  }
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}

	// Roots include the dangling-parent group.
	wantList(t, "roots", f.GroupChildren(""), []string{"root-a", "root-b", "stray"})
	wantList(t, "children of root-a", f.GroupChildren("root-a"), []string{"kid"})
	if got := f.GroupChildren("kid"); len(got) != 0 {
		t.Errorf("children of kid = %v, want none", got)
	}

	wantList(t, "devices of kid", f.GroupDevices("kid"), []string{"d1", "d2"})
	wantList(t, "devices of root-b", f.GroupDevices("root-b"), []string{"d2"})
	wantList(t, "tags", f.DeviceTags(), []string{"d1", "d2", "d3"})

	// Dangling parent cuts the ancestry at the highest resolvable group.
	wantList(t, "stray ancestry", f.GroupAncestry("stray"), []string{"stray"})
	// Unknown group has no ancestry.
	if got := f.GroupAncestry("ghost"); len(got) != 0 {
		t.Errorf("ghost ancestry = %v", got)
	}
}

func TestGroupAncestryCycleGuard(t *testing.T) {
	// A hand-edited document can contain a parent cycle; traversal must
	// terminate and produce a finite chain.
	const j = `{"version":3,"groups":{"a":{"parent":"b"},"b":{"parent":"a"}},"devices":{}}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	got := f.GroupAncestry("a")
	if len(got) != 2 {
		t.Fatalf("cyclic ancestry = %v, want finite 2-chain", got)
	}
}

func TestResolveSortedAndValues(t *testing.T) {
	f := load(t)
	sorted := f.ResolveSorted("dev-a")
	if len(sorted) == 0 {
		t.Fatal("no resolutions")
	}
	for i := 1; i < len(sorted); i++ {
		if sorted[i-1].Key >= sorted[i].Key {
			t.Fatalf("not sorted: %s >= %s", sorted[i-1].Key, sorted[i].Key)
		}
	}
	vals := f.ResolveValues("dev-a")
	if vals["secureboot"] != false {
		t.Errorf("ResolveValues secureboot = %v, want false (org-enforced)", vals["secureboot"])
	}
	if len(vals) != len(sorted) {
		t.Errorf("values %d != sorted %d", len(vals), len(sorted))
	}
}

func TestAcceptanceScopeErrors(t *testing.T) {
	f := load(t)
	if err := SetAcceptance("group:ghost", "k", "reason")(f); err == nil {
		t.Error("unknown group accepted")
	}
	if err := SetAcceptance("device:ghost", "k", "reason")(f); err == nil {
		t.Error("unknown device accepted")
	}
	if err := SetAcceptance("cosmos", "k", "reason")(f); err == nil {
		t.Error("malformed ref accepted")
	}
	// Group-level acceptance resolves for members.
	if err := SetAcceptance("group:pilot", "ctrl", "pilot risk ok")(f); err != nil {
		t.Fatal(err)
	}
	if r, sc, ok := f.AcceptanceFor("dev-a", "ctrl"); !ok || sc != "group:pilot" || r != "pilot risk ok" {
		t.Errorf("group acceptance = (%q,%q,%v)", r, sc, ok)
	}
	if err := ClearAcceptance("group:pilot", "ctrl")(f); err != nil {
		t.Fatal(err)
	}
	// Unknown device: no acceptance, no panic.
	if _, _, ok := f.AcceptanceFor("ghost", "ctrl"); ok {
		t.Error("acceptance for unknown device")
	}
}

func TestValidateFlatpakMirrorsPackage(t *testing.T) {
	if !ValidateFlatpak("org.mozilla.firefox") || ValidateFlatpak("bad id") || ValidateFlatpak("a..b") {
		t.Error("flatpak validator wrong")
	}
}

func TestReleasedGroupDevices(t *testing.T) {
	f := &Fleet{
		Version: Version,
		Groups:  map[string]Group{"fleet": {}},
		Devices: map[string]Device{
			"d1": {Groups: []string{"fleet"}, Pin: "fleet"},
			"d2": {Groups: []string{"fleet"}}, // not released
			"d3": {Groups: []string{"fleet"}, Pin: "fleet"},
			"r1": {Groups: []string{"fleet"}, Pin: "fleet", State: DeviceRetired}, // retired: excluded
		},
	}
	// Uncapped (no rollout ring): whole active group.
	if got := f.ReleasedGroupDevices("fleet"); len(got) != 3 {
		t.Fatalf("uncapped = %v, want 3 active", got)
	}
	// Capped wave: only devices pinned to the ring, deterministic order.
	f.Rollout = &RolloutPolicy{Rings: []RolloutRing{{Group: "fleet", MaxDevices: 2}}}
	got := f.ReleasedGroupDevices("fleet")
	if len(got) != 2 || got[0] != "d1" || got[1] != "d3" {
		t.Fatalf("capped released = %v, want [d1 d3]", got)
	}
}
