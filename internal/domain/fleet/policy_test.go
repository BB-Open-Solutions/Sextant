package fleet

import "testing"

// policyFleet builds a fleet with a group tree, devices with filterable
// attributes, and no policies yet.
func policyFleet(t *testing.T) *Fleet {
	t.Helper()
	const j = `{
	  "version": 3,
	  "org": {"settings": {"desktop": "plasma"}},
	  "groups": {
	    "zaanstad":    {},
	    "frontoffice": {"parent": "zaanstad"}
	  },
	  "devices": {
	    "lt-1": {"groups": ["frontoffice"], "hardware": "hp-g4", "class": "laptop", "assignedUser": "ada"},
	    "lt-2": {"groups": ["frontoffice"], "hardware": "t495s", "class": "laptop"},
	    "srv-1": {"groups": ["zaanstad"], "hardware": "msi", "class": "server", "labels": {"site": "inspoelstraat"}}
	  }
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func apply(t *testing.T, f *Fleet, ms ...Mutation) {
	t.Helper()
	for _, m := range ms {
		if err := m(f); err != nil {
			t.Fatal(err)
		}
	}
}

func TestPolicyDefault_ScopeSpecificityStillWins(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("baseline", Policy{Settings: map[string]any{"apps.office": true}}),
		Assign(Assignment{Policy: "baseline", Target: "org"}),
	)
	// Policy at org delivers the default...
	want(t, f.Resolve("lt-1"), "apps.office", true, "policy:baseline@org", false)

	// ...but an inline group setting is more specific and wins.
	apply(t, f, SetScopeSetting("group:frontoffice", "apps.office", false))
	want(t, f.Resolve("lt-1"), "apps.office", false, "group:frontoffice", false)
}

func TestPolicyEnforced_GeneralBeatsSpecific(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("hardening", Policy{
			Settings: map[string]any{"secureboot": true},
			Enforced: []string{"secureboot"},
		}),
		Assign(Assignment{Policy: "hardening", Target: "org"}),
		SetScopeSetting("device:lt-1", "secureboot", false),
	)
	// The org-assigned policy enforces secureboot; the device cannot override.
	want(t, f.Resolve("lt-1"), "secureboot", true, "policy:hardening@org", true)
}

func TestPolicyAtParentGroupFlowsToSubgroupDevices(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("comms", Policy{Settings: map[string]any{"apps.comms": true}}),
		Assign(Assignment{Policy: "comms", Target: "group:zaanstad"}),
	)
	// lt-1 is in frontoffice, a child of zaanstad: the policy applies.
	want(t, f.Resolve("lt-1"), "apps.comms", true, "policy:comms@group:zaanstad", false)
	// srv-1 is directly in zaanstad: applies too.
	want(t, f.Resolve("srv-1"), "apps.comms", true, "policy:comms@group:zaanstad", false)
}

func TestPolicyFilterNarrowsAssignment(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutFilter("laptops", Filter{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: "laptop"}}}),
		PutPolicy("vpn", Policy{Settings: map[string]any{"netbird.enable": true}}),
		Assign(Assignment{Policy: "vpn", Target: "org", Filter: "laptops"}),
	)
	// Laptops get the policy...
	want(t, f.Resolve("lt-1"), "netbird.enable", true, "policy:vpn@org", false)
	// ...the server does not.
	if _, has := f.Resolve("srv-1")["netbird.enable"]; has {
		t.Fatal("filtered policy leaked to a non-matching device")
	}
}

func TestPolicyPriorityBreaksTies(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("base", Policy{Settings: map[string]any{"desktop": "gnome"}}),
		PutPolicy("special", Policy{Settings: map[string]any{"desktop": "plasma"}}),
		Assign(Assignment{Policy: "base", Target: "group:frontoffice", Priority: 1}),
		Assign(Assignment{Policy: "special", Target: "group:frontoffice", Priority: 5}),
	)
	// Same scope, both defaults: higher priority wins.
	want(t, f.Resolve("lt-1"), "desktop", "plasma", "policy:special@group:frontoffice", false)
}

func TestPolicyDeterministicOrderOnEqualPriority(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("p1", Policy{Settings: map[string]any{"x": "a"}}),
		PutPolicy("p2", Policy{Settings: map[string]any{"x": "b"}}),
		Assign(Assignment{Policy: "p1", Target: "org"}),
		Assign(Assignment{Policy: "p2", Target: "org"}),
	)
	// Equal specificity and priority: the earlier assignment wins,
	// deterministically, on every call.
	first := f.Resolve("lt-1")["x"]
	for i := 0; i < 20; i++ {
		if got := f.Resolve("lt-1")["x"]; got != first {
			t.Fatal("resolution is not deterministic")
		}
	}
	if first.Source.Policy != "p1" {
		t.Fatalf("winner = %s, want first assignment p1", first.Source)
	}
}

func TestDeviceTargetedAssignment(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("kiosk", Policy{Settings: map[string]any{"kiosk.enable": true}}),
		Assign(Assignment{Policy: "kiosk", Target: "device:srv-1"}),
	)
	want(t, f.Resolve("srv-1"), "kiosk.enable", true, "policy:kiosk@device", false)
	if _, has := f.Resolve("lt-1")["kiosk.enable"]; has {
		t.Fatal("device-targeted policy leaked to another device")
	}
}

func TestPolicyReferentialIntegrity(t *testing.T) {
	f := policyFleet(t)
	apply(t, f, PutPolicy("p", Policy{Settings: map[string]any{"x": 1}}))

	// Assignment must reference existing policy, target and filter.
	if err := Assign(Assignment{Policy: "nope", Target: "org"})(f); err == nil {
		t.Error("unknown policy accepted")
	}
	if err := Assign(Assignment{Policy: "p", Target: "group:nope"})(f); err == nil {
		t.Error("unknown target group accepted")
	}
	if err := Assign(Assignment{Policy: "p", Target: "device:nope"})(f); err == nil {
		t.Error("unknown target device accepted")
	}
	if err := Assign(Assignment{Policy: "p", Target: "borg"})(f); err == nil {
		t.Error("malformed target accepted")
	}
	if err := Assign(Assignment{Policy: "p", Target: "org", Filter: "nope"})(f); err == nil {
		t.Error("unknown filter accepted")
	}

	// A policy in use cannot be deleted; unassign frees it.
	apply(t, f, Assign(Assignment{Policy: "p", Target: "org"}))
	if err := Assign(Assignment{Policy: "p", Target: "org"})(f); err == nil {
		t.Error("duplicate assignment accepted")
	}
	if err := DeletePolicy("p")(f); err == nil {
		t.Error("assigned policy deleted")
	}
	apply(t, f, Unassign("p", "org", ""))
	if err := Unassign("p", "org", "")(f); err == nil {
		t.Error("double unassign accepted")
	}
	apply(t, f, DeletePolicy("p"))

	// A filter in use cannot be deleted.
	apply(t, f,
		PutFilter("fl", Filter{Rules: []FilterRule{{Attr: AttrTag, Op: OpEq, Value: "lt-1"}}}),
		PutPolicy("q", Policy{Settings: map[string]any{"y": 1}}),
		Assign(Assignment{Policy: "q", Target: "org", Filter: "fl"}),
	)
	if err := DeleteFilter("fl")(f); err == nil {
		t.Error("in-use filter deleted")
	}
	if err := DeleteFilter("nope")(f); err == nil {
		t.Error("unknown filter deleted without error")
	}
}

func TestPutPolicyValidation(t *testing.T) {
	f := policyFleet(t)
	if err := PutPolicy("Bad_ID", Policy{Settings: map[string]any{"x": 1}})(f); err == nil {
		t.Error("bad policy id accepted")
	}
	if err := PutPolicy("empty", Policy{})(f); err == nil {
		t.Error("empty policy accepted")
	}
	if err := PutPolicy("p", Policy{
		Settings: map[string]any{"x": 1},
		Enforced: []string{"y"},
	})(f); err == nil {
		t.Error("policy enforcing an unset key accepted")
	}
	if err := DeletePolicy("ghost")(f); err == nil {
		t.Error("deleting unknown policy accepted")
	}
}

func TestDanglingReferencesFailClosed(t *testing.T) {
	// A document that arrives with dangling references (hand-edited in git)
	// must resolve without the broken parts, never panic or over-apply.
	const j = `{
	  "version": 3,
	  "groups": {"g": {}},
	  "devices": {"d": {"groups": ["g"], "hardware": "hw"}},
	  "policies": {"p": {"settings": {"x": 1}}},
	  "assignments": [
	    {"policy": "ghost", "target": "org"},
	    {"policy": "p", "target": "org", "filter": "ghost"}
	  ]
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	r := f.Resolve("d")
	if len(r) != 0 {
		t.Fatalf("dangling assignment applied: %v", r)
	}
}
