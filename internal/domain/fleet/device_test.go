package fleet

import "testing"

func TestAddRemoveDevice(t *testing.T) {
	f := policyFleet(t)

	if err := AddDevice("lt-new", Device{Hardware: "hp-g4", Groups: []string{"frontoffice"}, Class: "laptop"})(f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Devices["lt-new"]; !ok {
		t.Fatal("device not added")
	}
	// Inherits the tree: org desktop resolves for the new device.
	if v := f.Resolve("lt-new")["desktop"]; v.Value != "plasma" {
		t.Fatalf("new device resolve = %+v", v)
	}

	bad := []struct {
		tag string
		d   Device
	}{
		{"UPPER", Device{Hardware: "hw"}},
		{"x/../y", Device{Hardware: "hw"}},
		{"lt-new", Device{Hardware: "hw"}},                                // duplicate
		{"no-hw", Device{}},                                               // hardware required
		{"ghost-group", Device{Hardware: "hw", Groups: []string{"nope"}}}, // unknown group
	}
	for _, tc := range bad {
		if err := AddDevice(tc.tag, tc.d)(f); err == nil {
			t.Errorf("AddDevice(%q) accepted", tc.tag)
		}
	}

	// Remove drops device-targeted assignments too.
	apply(t, f,
		PutPolicy("p", Policy{Settings: map[string]any{"x": 1}}),
		Assign(Assignment{Policy: "p", Target: "device:lt-new"}),
		RemoveDevice("lt-new"),
	)
	if len(f.Assignments) != 0 {
		t.Fatal("device assignment survived removal")
	}
	if err := RemoveDevice("lt-new")(f); err == nil {
		t.Fatal("double remove accepted")
	}
}

func TestAddGroup(t *testing.T) {
	f := policyFleet(t)
	if err := AddGroup("backoffice", Group{Parent: "zaanstad"})(f); err != nil {
		t.Fatal(err)
	}
	got := f.GroupAncestry("backoffice")
	if len(got) != 2 || got[0] != "zaanstad" {
		t.Fatalf("ancestry = %v", got)
	}
	if err := AddGroup("backoffice", Group{})(f); err == nil {
		t.Error("duplicate group accepted")
	}
	if err := AddGroup("orphan", Group{Parent: "ghost"})(f); err == nil {
		t.Error("unknown parent accepted")
	}
	if err := AddGroup("Bad Name", Group{})(f); err == nil {
		t.Error("bad slug accepted")
	}
}

func TestGroupMembershipDelta(t *testing.T) {
	got := GroupMembershipDelta([]string{"a", "b"}, []string{"b", "c"})
	// symmetric difference: a left, c joined; b unchanged (not in result)
	set := map[string]bool{}
	for _, g := range got {
		set[g] = true
	}
	if len(got) != 2 || !set["a"] || !set["c"] || set["b"] {
		t.Fatalf("delta = %v, want {a, c}", got)
	}
	if d := GroupMembershipDelta([]string{"x"}, []string{"x"}); len(d) != 0 {
		t.Fatalf("no change should be empty, got %v", d)
	}
}
