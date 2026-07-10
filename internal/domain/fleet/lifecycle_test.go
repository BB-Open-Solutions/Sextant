package fleet

import "testing"

// lifecycleFleet: one group tree with two devices for lifecycle and
// management-mutation tests.
func lifecycleFleet() *Fleet {
	return &Fleet{
		Version: 3,
		Groups:  map[string]Group{"alpha": {}, "alpha-front": {Parent: "alpha"}},
		Devices: map[string]Device{
			"lt-1": {Groups: []string{"alpha-front"}, Hardware: "hw"},
			"lt-2": {Groups: []string{"alpha"}, Hardware: "hw"},
		},
	}
}

func TestUpdateDevice(t *testing.T) {
	f := lifecycleFleet()
	hw, user := "hw-v2", "ada"
	groups := []string{"alpha"}
	labels := map[string]string{"site": "hq"}
	err := UpdateDevice("lt-1", DevicePatch{
		Hardware: &hw, AssignedUser: &user, Groups: &groups, Labels: &labels,
	})(f)
	if err != nil {
		t.Fatal(err)
	}
	d := f.Devices["lt-1"]
	if d.Hardware != "hw-v2" || d.AssignedUser != "ada" ||
		len(d.Groups) != 1 || d.Groups[0] != "alpha" || d.Labels["site"] != "hq" {
		t.Fatalf("device = %+v", d)
	}
	// Untouched fields survive a partial patch.
	if d.Class != "" || d.State != DeviceActive {
		t.Fatalf("patch touched unrelated fields: %+v", d)
	}
	empty := ""
	if err := UpdateDevice("lt-1", DevicePatch{Hardware: &empty})(f); err == nil {
		t.Error("empty hardware accepted")
	}
	ghost := []string{"ghost"}
	if err := UpdateDevice("lt-1", DevicePatch{Groups: &ghost})(f); err == nil {
		t.Error("unknown group accepted")
	}
	if err := UpdateDevice("nope", DevicePatch{})(f); err == nil {
		t.Error("unknown device accepted")
	}
}

func TestRetireReactivate(t *testing.T) {
	f := lifecycleFleet()
	if err := RetireDevice("lt-1")(f); err != nil {
		t.Fatal(err)
	}
	if !f.Devices["lt-1"].Retired() {
		t.Fatal("not retired")
	}
	// Idempotence violations are errors, not silent no-ops: state changes
	// are audited commits and a double retire is a caller bug.
	if err := RetireDevice("lt-1")(f); err == nil {
		t.Error("double retire accepted")
	}
	if err := ReactivateDevice("lt-2")(f); err == nil {
		t.Error("reactivating an active device accepted")
	}
	if err := ReactivateDevice("lt-1")(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].Retired() {
		t.Fatal("still retired")
	}
	if err := RetireDevice("ghost")(f); err == nil {
		t.Error("unknown device accepted")
	}
}

func TestActiveGroupDevicesSkipsRetired(t *testing.T) {
	f := lifecycleFleet()
	_ = RetireDevice("lt-2")(f)
	if got := f.GroupDevices("alpha"); len(got) != 1 {
		t.Fatalf("GroupDevices = %v (must keep retired for reference guards)", got)
	}
	if got := f.ActiveGroupDevices("alpha"); len(got) != 0 {
		t.Fatalf("ActiveGroupDevices = %v", got)
	}
}

func TestUpdateGroup(t *testing.T) {
	f := lifecycleFleet()
	idp := "idp-alpha-front"
	if err := UpdateGroup("alpha-front", nil, &idp)(f); err != nil {
		t.Fatal(err)
	}
	if f.Groups["alpha-front"].IdpGroup != idp {
		t.Fatal("idp mapping not set")
	}
	// Re-parent to root; cycle refused.
	root := ""
	if err := UpdateGroup("alpha-front", &root, nil)(f); err != nil {
		t.Fatal(err)
	}
	if f.Groups["alpha-front"].Parent != "" {
		t.Fatal("not detached")
	}
	self := "alpha-front"
	if err := UpdateGroup("alpha-front", &self, nil)(f); err == nil {
		t.Error("self-parent accepted")
	}
	if err := UpdateGroup("ghost", nil, &idp)(f); err == nil {
		t.Error("unknown group accepted")
	}
}

func TestRemoveGroupGuards(t *testing.T) {
	f := lifecycleFleet()
	// Children, devices, assignments, bindings and rings all block removal.
	if err := RemoveGroup("alpha")(f); err == nil {
		t.Error("removed group with children")
	}
	if err := RemoveGroup("alpha-front")(f); err == nil {
		t.Error("removed group with devices")
	}
	// Empty the leaf, then hang each reference on it in turn.
	groups := []string{"alpha"}
	_ = UpdateDevice("lt-1", DevicePatch{Groups: &groups})(f)

	f.Policies = map[string]Policy{"p": {Settings: map[string]any{"x": true}}}
	f.Assignments = []Assignment{{Policy: "p", Target: "group:alpha-front"}}
	if err := RemoveGroup("alpha-front")(f); err == nil {
		t.Error("removed group with assignment")
	}
	f.Assignments = nil

	f.Access = []AccessBinding{{Group: "team", Role: "viewer", Scope: "group:alpha-front"}}
	if err := RemoveGroup("alpha-front")(f); err == nil {
		t.Error("removed group with binding")
	}
	f.Access = nil

	f.Rollout = &RolloutPolicy{Rings: []RolloutRing{{Group: "alpha-front"}}}
	if err := RemoveGroup("alpha-front")(f); err == nil {
		t.Error("removed group in a ring")
	}
	f.Rollout = nil

	if err := RemoveGroup("alpha-front")(f); err != nil {
		t.Fatalf("clean leaf refused: %v", err)
	}
	if err := RemoveGroup("ghost")(f); err == nil {
		t.Error("unknown group accepted")
	}
}

func TestSetScopeApps(t *testing.T) {
	f := lifecycleFleet()
	err := SetScopeApps("group:alpha", AppPackages, []string{"vlc", "firefox", "vlc", " "})(f)
	if err != nil {
		t.Fatal(err)
	}
	got := f.Groups["alpha"].Packages
	if len(got) != 2 || got[0] != "firefox" || got[1] != "vlc" {
		t.Fatalf("packages = %v (want deduped, sorted)", got)
	}
	// Replace semantics: a shorter list wins.
	_ = SetScopeApps("group:alpha", AppPackages, []string{"vlc"})(f)
	if got := f.Groups["alpha"].Packages; len(got) != 1 {
		t.Fatalf("replace failed: %v", got)
	}
	// Injection firewall per kind.
	for _, bad := range []string{"a b", `x"y`, "p; rm", "../up"} {
		if err := SetScopeApps("org", AppPackages, []string{bad})(f); err == nil {
			t.Errorf("package %q accepted", bad)
		}
	}
	if err := SetScopeApps("org", AppOverlays, []string{"dotted.name"})(f); err == nil {
		t.Error("dotted overlay name accepted")
	}
	if err := SetScopeApps("org", "weird", []string{"x"})(f); err == nil {
		t.Error("unknown kind accepted")
	}
	if err := SetScopeApps("group:ghost", AppPackages, nil)(f); err == nil {
		t.Error("unknown scope accepted")
	}
}

func TestSetRolloutPlan(t *testing.T) {
	f := lifecycleFleet()
	plan := &RolloutPolicy{Rings: []RolloutRing{
		{Group: "alpha-front", SoakMinutes: 30, MinHealthyPercent: 90},
		{Group: "alpha"},
	}}
	if err := SetRolloutPlan(plan)(f); err != nil {
		t.Fatal(err)
	}
	if f.Rollout == nil || len(f.Rollout.Rings) != 2 {
		t.Fatalf("plan = %+v", f.Rollout)
	}
	for _, bad := range []*RolloutPolicy{
		{Rings: []RolloutRing{}},
		{Rings: []RolloutRing{{Group: "ghost"}}},
		{Rings: []RolloutRing{{Group: "alpha"}, {Group: "alpha"}}},
		{Rings: []RolloutRing{{Group: "alpha", SoakMinutes: -1}}},
		{Rings: []RolloutRing{{Group: "alpha", MinHealthyPercent: 101}}},
	} {
		if err := SetRolloutPlan(bad)(f); err == nil {
			t.Errorf("bad plan accepted: %+v", bad)
		}
	}
	if err := SetRolloutPlan(nil)(f); err != nil {
		t.Fatal(err)
	}
	if f.Rollout != nil {
		t.Fatal("nil plan did not clear")
	}
}

func TestSetAssurance(t *testing.T) {
	f := lifecycleFleet()
	if err := SetAssurance(Assurance{RequireFourEyes: true})(f); err != nil {
		t.Fatal(err)
	}
	if f.Assurance == nil || !f.Assurance.RequireFourEyes {
		t.Fatalf("assurance = %+v", f.Assurance)
	}
}
