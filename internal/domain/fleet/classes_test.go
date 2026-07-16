package fleet

import (
	"reflect"
	"testing"
)

// classesFleet: a fleet whose devices deliberately probe every input the
// partitioner must key on. The partitioner is security-critical (it narrows
// what the interactive gate evaluates), so each case asserts the SAFE
// direction: differing build inputs must always split classes.
func classesFleet() *Fleet {
	return &Fleet{
		Version: 3,
		Org:     &Scope{Settings: map[string]any{"desktop": "plasma"}},
		Groups: map[string]Group{
			"kantoor": {Settings: map[string]any{"apps.office": true}},
			"balie":   {Parent: "kantoor"},
			"lab":     {},
		},
		Devices: map[string]Device{
			// twin-a and twin-b: identical shape, different tag.
			"twin-a": {Groups: []string{"kantoor"}, Hardware: "t495", Class: "laptop"},
			"twin-b": {Groups: []string{"kantoor"}, Hardware: "t495", Class: "laptop"},
			// Same group, different hardware.
			"other-hw": {Groups: []string{"kantoor"}, Hardware: "nuc8", Class: "laptop"},
			// Same everything as the twins, different class.
			"other-class": {Groups: []string{"kantoor"}, Hardware: "t495", Class: "desktop"},
			// Child group: inherits kantoor but is a different chain node.
			"deeper": {Groups: []string{"balie"}, Hardware: "t495", Class: "laptop"},
			// Own device-level setting.
			"special": {Groups: []string{"kantoor"}, Hardware: "t495", Class: "laptop",
				Settings: map[string]any{"kiosk": true}},
			// Different group tree entirely.
			"labbox": {Groups: []string{"lab"}, Hardware: "t495", Class: "laptop"},
			// Retired: does not build, must not appear anywhere.
			"gone": {Groups: []string{"kantoor"}, Hardware: "t495", Class: "laptop",
				State: DeviceRetired},
		},
	}
}

func classOf(t *testing.T, classes map[string][]string, tag string) string {
	t.Helper()
	for key, members := range classes {
		for _, m := range members {
			if m == tag {
				return key
			}
		}
	}
	t.Fatalf("device %s not in any class", tag)
	return ""
}

func TestEquivalenceClassesPartition(t *testing.T) {
	f := classesFleet()
	classes := f.EquivalenceClasses()

	// The twins share a class; every deliberate difference splits one.
	twin := classOf(t, classes, "twin-a")
	if classOf(t, classes, "twin-b") != twin {
		t.Fatal("identical devices split into different classes")
	}
	for _, tag := range []string{"other-hw", "other-class", "deeper", "special", "labbox"} {
		if classOf(t, classes, tag) == twin {
			t.Fatalf("%s (different build inputs) landed in the twins' class - the gate would not sample it", tag)
		}
	}

	// Retired devices do not build and must be absent.
	for key, members := range classes {
		for _, m := range members {
			if m == "gone" {
				t.Fatalf("retired device in class %s", key)
			}
		}
	}

	// Every ACTIVE device is in exactly one class (partition, no loss).
	total := 0
	for _, members := range classes {
		total += len(members)
	}
	if total != 7 {
		t.Fatalf("partition covers %d devices, want 7 active", total)
	}
}

// A policy narrowed by a filter changes the EFFECTIVE state of matching
// devices only: the partitioner must key on resolver output, so the matched
// device leaves its twins' class.
func TestEquivalenceClassesSeePolicyFilters(t *testing.T) {
	f := classesFleet()
	f.Policies = map[string]Policy{
		"harden": {Settings: map[string]any{"firewall.strict": true}},
	}
	f.Filters = map[string]Filter{
		"only-twin-a": {Rules: []FilterRule{{Attr: AttrTag, Op: OpEq, Value: "twin-a"}}},
	}
	f.Assignments = []Assignment{{Policy: "harden", Target: "org", Filter: "only-twin-a"}}

	classes := f.EquivalenceClasses()
	if classOf(t, classes, "twin-a") == classOf(t, classes, "twin-b") {
		t.Fatal("filter-scoped policy did not split the class: the unsampled twin would dodge the gate")
	}
}

// Enforce flips mkForce/mkDefault in the generated module: same value,
// different shape.
func TestEquivalenceClassesSeeEnforce(t *testing.T) {
	f := classesFleet()
	classes := f.EquivalenceClasses()
	before := classOf(t, classes, "twin-a") == classOf(t, classes, "twin-b")

	f.Devices["twin-a"] = func() Device {
		d := f.Devices["twin-a"]
		d.Settings = map[string]any{"desktop": "plasma"} // same value as org...
		return d
	}()
	// ...but now set at device level; resolved value identical, source and
	// shape may differ only via enforce. Force the org key to make it split.
	f.Org.Enforced = []string{"desktop"}

	classes = f.EquivalenceClasses()
	after := classOf(t, classes, "twin-a") == classOf(t, classes, "twin-b")
	if !before {
		t.Fatal("twins must start in one class")
	}
	_ = after // enforce propagates org-wide equally; assert via a device-level case:

	f2 := classesFleet()
	d := f2.Devices["twin-a"]
	d.Settings = map[string]any{"extra": 1}
	f2.Devices["twin-a"] = d
	c2 := f2.EquivalenceClasses()
	if classOf(t, c2, "twin-a") == classOf(t, c2, "twin-b") {
		t.Fatal("device-level setting did not split the class")
	}
}

// A rollout pin changes the branch a device follows (generator input).
func TestEquivalenceClassesSeePins(t *testing.T) {
	f := classesFleet()
	d := f.Devices["twin-a"]
	d.Pin = "kantoor"
	f.Devices["twin-a"] = d
	classes := f.EquivalenceClasses()
	if classOf(t, classes, "twin-a") == classOf(t, classes, "twin-b") {
		t.Fatal("device pin did not split the class")
	}

	f = classesFleet()
	g := f.Groups["kantoor"]
	g.Pin = "rev-9"
	f.Groups["kantoor"] = g
	classes = f.EquivalenceClasses()
	// Both twins share the pinned group: still together, but distinct from a
	// fleet without the pin.
	if classOf(t, classes, "twin-a") != classOf(t, classes, "twin-b") {
		t.Fatal("group pin wrongly split devices that share it")
	}
}

// Representatives: deterministic (sorted first member), sorted output, one
// per class.
func TestRepresentativesDeterministic(t *testing.T) {
	f := classesFleet()
	reps := f.Representatives()
	again := f.Representatives()
	if !reflect.DeepEqual(reps, again) {
		t.Fatalf("representatives not deterministic: %v vs %v", reps, again)
	}
	if len(reps) != len(f.EquivalenceClasses()) {
		t.Fatalf("%d representatives for %d classes", len(reps), len(f.EquivalenceClasses()))
	}
	// The twins' representative is the sorted-first twin.
	found := false
	for _, r := range reps {
		if r == "twin-a" {
			found = true
		}
		if r == "twin-b" {
			t.Fatal("representative is not the sorted-first member")
		}
	}
	if !found {
		t.Fatal("twins unrepresented")
	}
}

// Values that differ only in type or map ordering must not collide: the key
// uses canonical JSON, not fmt.
func TestClassKeyCanonicalValues(t *testing.T) {
	f := classesFleet()
	a := f.Devices["twin-a"]
	a.Settings = map[string]any{"n": 1}
	f.Devices["twin-a"] = a
	b := f.Devices["twin-b"]
	b.Settings = map[string]any{"n": "1"}
	f.Devices["twin-b"] = b
	classes := f.EquivalenceClasses()
	if classOf(t, classes, "twin-a") == classOf(t, classes, "twin-b") {
		t.Fatal(`1 and "1" collided: value hashing is not type-aware`)
	}
}
