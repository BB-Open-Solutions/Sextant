package fleet

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// TestVisibleToRedactsOrgTargetedFilterRulesForNonOrgViewer guards a
// confidentiality leak: an org-targeted assignment's filter is kept (it
// governs the caller's own devices too), but its Rules can name a scope the
// caller cannot see - a group, a user, or a label. A viewer bound only to
// one unrelated group must learn that the filter exists (rendering needs
// that), never what it actually matches on.
func TestVisibleToRedactsOrgTargetedFilterRulesForNonOrgViewer(t *testing.T) {
	f := visFleet()
	// An org-targeted assignment gated by a filter naming a group the
	// alpha-team viewer cannot see.
	f.Filters["flt-org-secret"] = Filter{
		Name:  "finance-only",
		Match: MatchAll,
		Rules: []FilterRule{{Attr: AttrGroup, Op: OpEq, Value: "finance"}},
	}
	f.Assignments = append(f.Assignments, Assignment{
		Policy: "pol-org", Target: "org", Filter: "flt-org-secret",
	})

	got := f.VisibleTo(canViewFor(f, identity.User{Subject: "u", Groups: []string{"alpha-team"}}))

	fl, ok := got.Filters["flt-org-secret"]
	if !ok {
		t.Fatal("org-targeted filter must stay enumerable (the assignment governs the caller's devices)")
	}
	if fl.Name != "finance-only" {
		t.Errorf("filter name = %q, want preserved", fl.Name)
	}
	if len(fl.Rules) != 0 {
		t.Errorf("filter rules = %+v, want redacted (leaks group %q the viewer cannot see)", fl.Rules, "finance")
	}

	// A filter reachable through the caller's OWN visible scope keeps its
	// full rules - only org-only-reachable filters are redacted.
	if own, ok := got.Filters["flt-alpha"]; !ok || len(own.Rules) == 0 {
		t.Errorf("own-scope filter over-redacted: %+v, %v", own, ok)
	}
}
