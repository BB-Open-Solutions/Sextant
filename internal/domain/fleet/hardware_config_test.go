package fleet

import "testing"

func infraFleet() *Fleet {
	return &Fleet{
		Version: 3,
		Org:     &Scope{},
		Groups:  map[string]Group{"infra": {}},
		Devices: map[string]Device{
			"lt-lenovo": {Groups: []string{"infra"}, Hardware: "lenovo-t495s"},
			"lt-dell":   {Groups: []string{"infra"}, Hardware: "dell-latitude-5440"},
		},
	}
}

// The case this exists for: a driver the Lenovo needs and the Dell in the same
// group must not get. Group membership cannot say that, and per-device edits
// do not survive the fleet growing.
func TestConfiguringAModelReachesOnlyThatModel(t *testing.T) {
	f := infraFleet()
	err := ConfigureHardware("lenovo-t495s", "group:infra",
		map[string]any{"fprint.enable": true}, nil)(f)
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := f.ResolveValues("lt-lenovo")["fprint.enable"]; !ok || v != true {
		t.Fatal("the Lenovo did not get the driver")
	}
	if _, ok := f.ResolveValues("lt-dell")["fprint.enable"]; ok {
		t.Fatal("the Dell got a driver it does not need")
	}
}

// Configuring is editing: called again it refreshes the settings and leaves
// one assignment, not two.
func TestConfiguringAModelTwiceIsAnEdit(t *testing.T) {
	f := infraFleet()
	set := func(v any) error {
		return ConfigureHardware("lenovo-t495s", "org", map[string]any{"fprint.enable": v}, nil)(f)
	}
	if err := set(true); err != nil {
		t.Fatal(err)
	}
	if err := set(false); err != nil {
		t.Fatal(err)
	}
	if got := len(f.Assignments); got != 1 {
		t.Fatalf("assignments = %d, want one after configuring twice: %+v", got, f.Assignments)
	}
	if v := f.ResolveValues("lt-lenovo")["fprint.enable"]; v != false {
		t.Fatalf("fprint.enable = %v, want the second write to have landed", v)
	}
}

// Moving a model's settings from the org to one group must MOVE the
// assignment. A second one is not a wider rollout, it is the same value
// resolving twice and an operator wondering which one is live.
func TestChangingTheTargetMovesTheAssignment(t *testing.T) {
	f := infraFleet()
	s := map[string]any{"fprint.enable": true}
	if err := ConfigureHardware("lenovo-t495s", "org", s, nil)(f); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHardware("lenovo-t495s", "group:infra", s, nil)(f); err != nil {
		t.Fatal(err)
	}
	if len(f.Assignments) != 1 || f.Assignments[0].Target != "group:infra" {
		t.Fatalf("assignments = %+v, want one bound to group:infra", f.Assignments)
	}
}

// Emptying the settings removes the configuration. A policy that says nothing
// would be refused by PutPolicy anyway, and an assignment left behind with an
// empty policy is a rule that reaches devices and does nothing.
func TestEmptyingAModelRemovesItsConfiguration(t *testing.T) {
	f := infraFleet()
	if err := ConfigureHardware("lenovo-t495s", "org", map[string]any{"fprint.enable": true}, nil)(f); err != nil {
		t.Fatal(err)
	}
	if err := ConfigureHardware("lenovo-t495s", "org", nil, nil)(f); err != nil {
		t.Fatal(err)
	}
	if len(f.Assignments) != 0 || len(f.Policies) != 0 || len(f.Filters) != 0 {
		t.Fatalf("left behind: assignments=%+v policies=%+v filters=%+v", f.Assignments, f.Policies, f.Filters)
	}
	if _, ok := f.ResolveValues("lt-lenovo")["fprint.enable"]; ok {
		t.Fatal("the Lenovo still resolves a setting that was removed")
	}
}

// A filter with the derived name that means something else must not be
// silently reused: the model's settings would land on a different set of
// devices, and nothing on screen would say so.
func TestAFilterThatMeansSomethingElseIsRefused(t *testing.T) {
	f := infraFleet()
	f.Filters = map[string]Filter{
		HardwareFilterID("lenovo-t495s"): {Rules: []FilterRule{
			{Attr: AttrClass, Op: OpEq, Value: "laptop"}}},
	}
	err := ConfigureHardware("lenovo-t495s", "org", map[string]any{"fprint.enable": true}, nil)(f)
	if err == nil {
		t.Fatal("a filter selecting something else was reused")
	}
}

// A policy that came from an overlay profile is not ours to overwrite:
// re-applying the profile would fight this write on every drift repair.
func TestAProfilePolicyIsNotOverwritten(t *testing.T) {
	f := infraFleet()
	f.Policies = map[string]Policy{
		HardwarePolicyID("lenovo-t495s"): {Profile: "vendor@1", Settings: map[string]any{"a": 1}},
	}
	if err := ConfigureHardware("lenovo-t495s", "org", map[string]any{"fprint.enable": true}, nil)(f); err == nil {
		t.Fatal("a profile-owned policy was overwritten")
	}
}

// Model settings bind to the org or a group. Not to one device: that is what
// the device's own settings are for, and a model-wide policy bound to a single
// device is a way of writing it that nobody reading the fleet would recognise.
func TestModelSettingsDoNotBindToADevice(t *testing.T) {
	f := infraFleet()
	s := map[string]any{"fprint.enable": true}
	if err := ConfigureHardware("lenovo-t495s", "device:lt-lenovo", s, nil)(f); err == nil {
		t.Fatal("model settings bound to a single device")
	}
	if err := ConfigureHardware("lenovo-t495s", "group:ghosts", s, nil)(f); err == nil {
		t.Fatal("model settings bound to a group that does not exist")
	}
}

// Retired devices do not count towards a model's device count: a page about
// models should not report a shelf.
func TestHardwareInUseIgnoresRetiredDevices(t *testing.T) {
	f := infraFleet()
	f.Devices["lt-old"] = Device{Hardware: "lenovo-t495s", State: DeviceRetired}
	if got := f.HardwareInUse()["lenovo-t495s"]; got != 1 {
		t.Fatalf("count = %d, want the retired one left out", got)
	}
}
