package fleet

import (
	"slices"
	"strings"
	"testing"
	"time"
)

func TestAddRemoveDevice(t *testing.T) {
	f := policyFleet(t)

	if err := AddDevice("lt-new", Device{Hardware: "hp-g4", Groups: []string{"frontoffice"}, Class: "laptop"}, time.Now())(f); err != nil {
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
		if err := AddDevice(tc.tag, tc.d, time.Now())(f); err == nil {
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

func TestCreateGroupWithDevices(t *testing.T) {
	f := policyFleet(t)
	if err := CreateGroupWithDevices("pilot", Group{Parent: "zaanstad"}, []string{"lt-1", "lt-2"})(f); err != nil {
		t.Fatal(err)
	}
	if _, ok := f.Groups["pilot"]; !ok {
		t.Fatal("group not created")
	}
	for _, tag := range []string{"lt-1", "lt-2"} {
		if !slices.Contains(f.Devices[tag].Groups, "pilot") {
			t.Errorf("%s not moved into pilot: %v", tag, f.Devices[tag].Groups)
		}
	}
	// A device already in the group is not duplicated.
	if err := CreateGroupWithDevices("pilot2", Group{}, []string{"lt-1", "lt-1"})(f); err != nil {
		t.Fatal(err)
	}
	if n := slices.Contains(f.Devices["lt-1"].Groups, "pilot2"); !n {
		t.Error("lt-1 not in pilot2")
	}
	if got := f.Devices["lt-1"].Groups; len(got) != 3 { // frontoffice, pilot, pilot2
		t.Errorf("lt-1 groups = %v, want 3 unique", got)
	}
	// An unknown device fails the whole mutation.
	if err := CreateGroupWithDevices("nope", Group{}, []string{"ghost"})(f); err == nil {
		t.Error("unknown device accepted")
	}
	// A duplicate group name fails (reuses AddGroup's guard).
	if err := CreateGroupWithDevices("pilot", Group{}, []string{"lt-1"})(f); err == nil {
		t.Error("duplicate group accepted")
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

// TestEnrolmentChecksTheClassVocabulary pins the fix of 2026-08-07. The
// vocabulary was enforced when CHANGING a class and not when enrolling, so a
// device could be created as anything and then refused when somebody tried
// to correct it - bypassable at the one moment it is easiest to get wrong.
//
// Absent and wrong are different, and only wrong is refused: a device
// discovered by a station has no class until an operator gives it one.
func TestEnrolmentChecksTheClassVocabulary(t *testing.T) {
	base := func() *Fleet {
		return &Fleet{Groups: map[string]Group{"pilot": {}}, Devices: map[string]Device{}}
	}

	for _, class := range Classes {
		f := base()
		if err := AddDevice("lt-1", Device{Hardware: "hp-g4", Class: class}, time.Now())(f); err != nil {
			t.Errorf("a known class %q was refused: %v", class, err)
		}
	}

	f := base()
	if err := AddDevice("lt-2", Device{Hardware: "hp-g4", Class: ""}, time.Now())(f); err != nil {
		t.Errorf("enrolling without a class was refused: %v", err)
	}

	f = base()
	err := AddDevice("lt-3", Device{Hardware: "hp-g4", Class: "kiosk"}, time.Now())(f)
	if err == nil {
		t.Fatal("a class outside the vocabulary was accepted at enrolment")
	}
	// The message has to list the choices, or an operator has to read source
	// to find out what is allowed.
	for _, want := range Classes {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the error does not offer %q: %v", want, err)
		}
	}
	if _, ok := f.Devices["lt-3"]; ok {
		t.Error("the refused device was added anyway")
	}
}
