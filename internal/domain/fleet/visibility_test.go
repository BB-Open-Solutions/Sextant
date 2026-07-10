package fleet

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// visFleet: two sibling subtrees under org, policies assigned to each, a
// rollout plan and per-scope bindings - the shape confidentiality must cut.
func visFleet() *Fleet {
	return &Fleet{
		Version: 3,
		Org:     &Scope{Settings: map[string]any{"desktop": "plasma"}},
		Groups: map[string]Group{
			"alpha":       {},
			"alpha-front": {Parent: "alpha"},
			"beta":        {},
		},
		Devices: map[string]Device{
			"a-1": {Groups: []string{"alpha-front"}},
			"b-1": {Groups: []string{"beta"}},
		},
		Policies: map[string]Policy{
			"pol-alpha":  {Settings: map[string]any{"apps.office": true}},
			"pol-beta":   {Settings: map[string]any{"apps.media": true}},
			"pol-org":    {Settings: map[string]any{"secureboot": true}},
			"pol-orphan": {Settings: map[string]any{"secret": "x"}},
		},
		Filters: map[string]Filter{
			"flt-alpha": {Rules: []FilterRule{{Attr: "tag", Op: "eq", Value: "a-1"}}},
			"flt-beta":  {Rules: []FilterRule{{Attr: "tag", Op: "eq", Value: "b-1"}}},
		},
		Assignments: []Assignment{
			{Policy: "pol-alpha", Target: "group:alpha", Filter: "flt-alpha"},
			{Policy: "pol-beta", Target: "group:beta", Filter: "flt-beta"},
			{Policy: "pol-org", Target: "org"},
		},
		Access: []AccessBinding{
			{Group: "admins", Role: "owner", Scope: "org"},
			{Group: "alpha-team", Role: "viewer", Scope: "group:alpha"},
			{Group: "beta-team", Role: "viewer", Scope: "group:beta"},
		},
		Rollout: &RolloutPolicy{Rings: []RolloutRing{{Group: "beta"}}},
	}
}

func canViewFor(f *Fleet, u identity.User) func(string) bool {
	rv := f.IdentityResolver(nil, nil, nil)
	return func(ref string) bool { return rv.RoleAt(u, ref).Meets(identity.Viewer) }
}

func TestVisibleToOrgViewerSeesEverything(t *testing.T) {
	f := visFleet()
	got := f.VisibleTo(canViewFor(f, identity.User{Subject: "a", Groups: []string{"admins"}}))
	if got != f {
		t.Fatal("org viewer must get the unfiltered document")
	}
}

func TestVisibleToGroupViewerIsScoped(t *testing.T) {
	f := visFleet()
	got := f.VisibleTo(canViewFor(f, identity.User{Subject: "u", Groups: []string{"alpha-team"}}))

	// Own subtree stays: bound group, its child, the child's device.
	for _, g := range []string{"alpha", "alpha-front"} {
		if _, ok := got.Groups[g]; !ok {
			t.Errorf("group %s missing", g)
		}
	}
	if _, ok := got.Devices["a-1"]; !ok {
		t.Error("own device missing")
	}
	// The sibling subtree is gone: group, device, policy, filter, binding.
	if _, ok := got.Groups["beta"]; ok {
		t.Error("sibling group leaked")
	}
	if _, ok := got.Devices["b-1"]; ok {
		t.Error("sibling device leaked")
	}
	if _, ok := got.Policies["pol-beta"]; ok {
		t.Error("sibling policy leaked")
	}
	if _, ok := got.Filters["flt-beta"]; ok {
		t.Error("sibling filter leaked")
	}
	for _, b := range got.Access {
		if b.Group == "beta-team" {
			t.Error("sibling binding leaked")
		}
	}
	// Unreferenced policies never show.
	if _, ok := got.Policies["pol-orphan"]; ok {
		t.Error("orphan policy leaked")
	}
	// Org-level context stays: root settings, org assignment+policy, org binding.
	if got.Org == nil || got.Org.Settings["desktop"] != "plasma" {
		t.Error("org settings missing")
	}
	if _, ok := got.Policies["pol-org"]; !ok {
		t.Error("org-assigned policy missing")
	}
	if len(got.Assignments) != 2 {
		t.Errorf("assignments = %+v", got.Assignments)
	}
	hasOrgBinding := false
	for _, b := range got.Access {
		if b.Group == "admins" {
			hasOrgBinding = true
		}
	}
	if !hasOrgBinding {
		t.Error("org binding missing")
	}
	// Rollout enumerates groups: hidden from non-org viewers.
	if got.Rollout != nil {
		t.Error("rollout leaked")
	}
	// The original document is untouched.
	if len(f.Groups) != 3 || len(f.Devices) != 2 || f.Rollout == nil {
		t.Error("VisibleTo mutated its receiver")
	}
}
