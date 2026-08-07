package fleet

import "testing"

// scope_pin_test.go covers ScopeSettings and the two pin setters, which were
// at 0%.
//
// ScopeSettings is the read behind every settings page and every API scope
// read. It returns COPIES, and that is the property worth pinning: the
// caller renders and edits what it gets back, and a shared map would let a
// page render turn into a fleet mutation nobody committed.

func scopeFixture() *Fleet {
	return &Fleet{
		Org: &Scope{
			Settings: map[string]any{"desktop": "plasma", "secureboot": true},
			Enforced: []string{"secureboot"},
		},
		Groups: map[string]Group{
			"pilot": {Settings: map[string]any{"desktop": "gnome"}, Enforced: []string{"desktop"}},
			"bare":  {},
		},
		Devices: map[string]Device{
			"lt-1": {Groups: []string{"pilot"}, Settings: map[string]any{"apps.office": true}},
			"lt-2": {Groups: []string{"pilot"}},
		},
	}
}

func TestScopeSettingsReadsEachScope(t *testing.T) {
	f := scopeFixture()

	got, enf, err := f.ScopeSettings("org")
	if err != nil {
		t.Fatalf("org: %v", err)
	}
	if got["desktop"] != "plasma" || len(enf) != 1 || enf[0] != "secureboot" {
		t.Errorf("org = %v enforced %v", got, enf)
	}

	if got, enf, err = f.ScopeSettings("group:pilot"); err != nil {
		t.Fatalf("group: %v", err)
	}
	if got["desktop"] != "gnome" || len(enf) != 1 {
		t.Errorf("group = %v enforced %v", got, enf)
	}

	if got, _, err = f.ScopeSettings("device:lt-1"); err != nil {
		t.Fatalf("device: %v", err)
	}
	if got["apps.office"] != true {
		t.Errorf("device = %v", got)
	}

	// A scope that exists but holds nothing is not an error: that is the
	// normal state of a fresh group, and the settings page must render it.
	if got, enf, err = f.ScopeSettings("group:bare"); err != nil {
		t.Errorf("empty group errored: %v", err)
	} else if len(got) != 0 || len(enf) != 0 {
		t.Errorf("empty group returned %v / %v", got, enf)
	}
}

func TestScopeSettingsRefusesWhatItCannotResolve(t *testing.T) {
	f := scopeFixture()
	for _, ref := range []string{
		"group:ghosts",
		"device:no-such-device",
		"",
		"orgs",
		"group:",
		"device:",
		"tenant:x",
		// Deliberately: no bare name. Accepting "pilot" would make a typo in
		// a URL address a scope the caller never named.
		"pilot",
	} {
		t.Run(ref, func(t *testing.T) {
			if _, _, err := f.ScopeSettings(ref); err == nil {
				t.Errorf("resolved %q", ref)
			}
		})
	}
}

// TestScopeSettingsReturnsCopies is the one that protects the caller. Every
// settings render calls this and then edits what it got; if the maps were
// shared, a page render would mutate the fleet in memory, and the next
// reader would see changes that were never committed and never audited.
func TestScopeSettingsReturnsCopies(t *testing.T) {
	f := scopeFixture()
	got, enf, err := f.ScopeSettings("org")
	if err != nil {
		t.Fatal(err)
	}
	got["desktop"] = "MUTATED"
	got["brand-new"] = true
	if len(enf) > 0 {
		enf[0] = "MUTATED"
	}

	if f.Org.Settings["desktop"] != "plasma" {
		t.Error("mutating the returned map changed the fleet document")
	}
	if _, ok := f.Org.Settings["brand-new"]; ok {
		t.Error("adding to the returned map added to the fleet document")
	}
	if f.Org.Enforced[0] != "secureboot" {
		t.Error("mutating the returned enforced slice changed the fleet document")
	}
}

func TestSetGroupPinAndSetDevicePin(t *testing.T) {
	f := scopeFixture()

	// A group pin is what a ring promotion writes: the audit record and the
	// marker that tells FollowHead to leave this branch alone.
	if err := SetGroupPin("pilot", "rev-abc")(f); err != nil {
		t.Fatalf("SetGroupPin: %v", err)
	}
	if f.Groups["pilot"].Pin != "rev-abc" {
		t.Errorf("group pin = %q", f.Groups["pilot"].Pin)
	}
	// An empty target clears it, which is how a ring goes back to following
	// head after a run finishes.
	if err := SetGroupPin("pilot", "")(f); err != nil {
		t.Fatal(err)
	}
	if f.Groups["pilot"].Pin != "" {
		t.Errorf("group pin not cleared: %q", f.Groups["pilot"].Pin)
	}

	// A device pin releases one machine into a capped wave's cohort.
	if err := SetDevicePin("lt-1", "pilot")(f); err != nil {
		t.Fatalf("SetDevicePin: %v", err)
	}
	if f.Devices["lt-1"].Pin != "pilot" {
		t.Errorf("device pin = %q", f.Devices["lt-1"].Pin)
	}
	if err := SetDevicePin("lt-1", "")(f); err != nil {
		t.Fatal(err)
	}
	if f.Devices["lt-1"].Pin != "" {
		t.Errorf("device pin not cleared: %q", f.Devices["lt-1"].Pin)
	}

	// Pinning something that does not exist must fail rather than create it:
	// the rollout engine walks the plan, and a pin that silently invented a
	// group would put a ring in the document that no plan produced.
	if err := SetGroupPin("ghosts", "rev")(f); err == nil {
		t.Error("pinned a group that does not exist")
	}
	if err := SetDevicePin("no-such-device", "pilot")(f); err == nil {
		t.Error("pinned a device that does not exist")
	}
	if _, ok := f.Groups["ghosts"]; ok {
		t.Error("a failed pin created the group anyway")
	}
}
