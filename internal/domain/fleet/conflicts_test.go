package fleet

import "testing"

// Two policies claiming one key at one scope is a modelling mistake, and ADR
// 0026 says the console reports it rather than settling it with a number
// nobody can explain later. These tests pin what counts as a collision - and,
// just as importantly, what does not.
func TestConflictsAreReported(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("base", Policy{Settings: map[string]any{"desktop": "gnome", "secureboot": true}}),
		PutPolicy("special", Policy{Settings: map[string]any{"desktop": "plasma", "secureboot": true}}),
		Assign(Assignment{Policy: "base", Target: "group:frontoffice"}),
		Assign(Assignment{Policy: "special", Target: "group:frontoffice"}),
	)
	got := f.PolicyConflicts()
	if len(got) != 1 {
		t.Fatalf("conflicts = %+v, want exactly one (desktop)", got)
	}
	c := got[0]
	if c.Key != "desktop" {
		t.Errorf("key = %q, want desktop", c.Key)
	}
	// secureboot is claimed by both AND agreed on. Reporting agreement would
	// fill the list with things nobody has to act on, and a list like that
	// gets ignored wholesale.
	if c.Target != "group:frontoffice" {
		t.Errorf("target = %q", c.Target)
	}
	// The winner is the one resolution actually applies, or the report would
	// point at the wrong policy to go and fix.
	if c.Winner != "base" || c.Loser != "special" {
		t.Errorf("winner/loser = %s/%s, want base/special", c.Winner, c.Loser)
	}
	want(t, f.Resolve("lt-1"), "desktop", "gnome", "policy:base@group:frontoffice", false)
}

// Different scopes are not a conflict: that is what specificity is for, and
// calling it a conflict would flag the ordinary case of a group overriding
// the organisation.
func TestDifferentScopesAreNotAConflict(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("org-wide", Policy{Settings: map[string]any{"desktop": "gnome"}}),
		PutPolicy("front", Policy{Settings: map[string]any{"desktop": "plasma"}}),
		Assign(Assignment{Policy: "org-wide", Target: "org"}),
		Assign(Assignment{Policy: "front", Target: "group:frontoffice"}),
	)
	if got := f.PolicyConflicts(); len(got) != 0 {
		t.Errorf("conflicts = %+v, want none", got)
	}
	// And the more specific scope still wins, unchanged.
	want(t, f.Resolve("lt-1"), "desktop", "plasma", "policy:front@group:frontoffice", false)
}

// A filter on either side is reported with the conflict rather than used to
// dismiss it: two filters can be disjoint today and overlap tomorrow, and the
// operator is the one who knows which.
func TestConflictNamesTheFiltersInvolved(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutFilter("laptops", Filter{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: "laptop"}}}),
		PutPolicy("a", Policy{Settings: map[string]any{"desktop": "gnome"}}),
		PutPolicy("b", Policy{Settings: map[string]any{"desktop": "plasma"}}),
		Assign(Assignment{Policy: "a", Target: "org", Filter: "laptops"}),
		Assign(Assignment{Policy: "b", Target: "org"}),
	)
	got := f.PolicyConflicts()
	if len(got) != 1 || len(got[0].Filters) != 1 || got[0].Filters[0] != "laptops" {
		t.Fatalf("conflicts = %+v, want one naming the laptops filter", got)
	}
}

// A priority in an older fleet document keeps loading and does nothing. It is
// reported so the operator learns that, instead of discovering it when a
// device takes the value they thought they had outranked.
func TestInertPrioritiesAreReported(t *testing.T) {
	f := policyFleet(t)
	apply(t, f,
		PutPolicy("p", Policy{Settings: map[string]any{"desktop": "gnome"}}),
		Assign(Assignment{Policy: "p", Target: "org", Priority: 7}),
	)
	got := f.InertPriorities()
	if len(got) != 1 || got[0].Priority != 7 {
		t.Fatalf("inert priorities = %+v, want the one carrying 7", got)
	}
	f2 := policyFleet(t)
	apply(t, f2,
		PutPolicy("p", Policy{Settings: map[string]any{"desktop": "gnome"}}),
		Assign(Assignment{Policy: "p", Target: "org"}),
	)
	if got := f2.InertPriorities(); len(got) != 0 {
		t.Errorf("an assignment without a priority was reported: %+v", got)
	}
}
