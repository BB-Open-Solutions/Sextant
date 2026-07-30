package fleet

import (
	"slices"
	"testing"
)

// Governors answers "is this key already under governance here" for a whole
// scope, which is what the settings editor needs: it is opened on groups and
// on the organisation far more often than on one machine (ADR 0017).

func governedFleet() *Fleet {
	return &Fleet{
		Groups: map[string]Group{
			"nl":     {},
			"laptop": {Parent: "nl"},
			"kiosk":  {},
		},
		Devices: map[string]Device{
			"lt-1": {Groups: []string{"laptop"}},
			"ki-1": {Groups: []string{"kiosk"}},
		},
		Policies: map[string]Policy{
			"base":   {Name: "Baseline", Settings: map[string]any{"timeZone": "Europe/Amsterdam"}},
			"crypto": {Name: "Encryption", Settings: map[string]any{"diskEncryption": true}, Enforced: []string{"diskEncryption"}},
		},
		Assignments: []Assignment{
			{Policy: "base", Target: "org"},
			{Policy: "crypto", Target: "group:nl"},
		},
	}
}

func TestGovernorsReachDownTheScopeTree(t *testing.T) {
	f := governedFleet()
	for _, tc := range []struct {
		scope string
		want  []string // keys expected to be governed
	}{
		{"org", []string{"timeZone"}},
		{"group:nl", []string{"diskEncryption", "timeZone"}},
		{"group:laptop", []string{"diskEncryption", "timeZone"}}, // inherits nl
		{"group:kiosk", []string{"timeZone"}},                    // outside nl
		{"device:lt-1", []string{"diskEncryption", "timeZone"}},
		{"device:ki-1", []string{"timeZone"}},
	} {
		g := f.Governors(tc.scope)
		var got []string
		for k := range g {
			got = append(got, k)
		}
		slices.Sort(got)
		if !slices.Equal(got, tc.want) {
			t.Errorf("%s governed %v, want %v", tc.scope, got, tc.want)
		}
	}
}

// A policy assigned below a scope must not make that scope's own key look
// governed: it cannot change what the value here means, and saying otherwise
// calls a key governed on a page where the governance does not apply.
func TestGovernanceDoesNotReachUpwards(t *testing.T) {
	f := governedFleet()
	f.Assignments = append(f.Assignments, Assignment{Policy: "base", Target: "group:laptop"})
	if _, ok := f.Governors("org")["diskEncryption"]; ok {
		t.Error("a group-level policy governed the organisation scope")
	}
}

// Locked and merely contributed must stay distinguishable: one can still be
// overridden here, the other cannot, and an editor offering the same
// affordance for both is lying about one of them.
func TestLockedIsDistinctFromContributed(t *testing.T) {
	g := governedFleet().Governors("group:nl")
	if enc := g["diskEncryption"]; !enc.Enforced {
		t.Error("a policy that locks the key did not report it as enforced")
	}
	if tz := g["timeZone"]; tz.Enforced {
		t.Error("a policy that merely sets the key reported it as locked")
	}
}

// The names travel with the ids and must not drift onto each other when the
// list is sorted - the editor shows the name, and the wrong one sends the
// operator to the wrong policy.
func TestPolicyNamesStayPairedWithTheirIDs(t *testing.T) {
	f := governedFleet()
	// Two more policies on the same key, deliberately out of id order.
	f.Policies["zulu"] = Policy{Name: "Zulu", Settings: map[string]any{"timeZone": "UTC"}}
	f.Policies["alpha"] = Policy{Name: "Alpha", Settings: map[string]any{"timeZone": "UTC"}}
	f.Assignments = append(f.Assignments,
		Assignment{Policy: "zulu", Target: "org"},
		Assignment{Policy: "alpha", Target: "org"})

	g := f.Governors("org")["timeZone"]
	want := map[string]string{"alpha": "Alpha", "base": "Baseline", "zulu": "Zulu"}
	if len(g.Policies) != len(want) {
		t.Fatalf("got %v, want %d policies", g.Policies, len(want))
	}
	if !slices.IsSorted(g.Policies) {
		t.Errorf("policies %v are not sorted; the order must be stable across renders", g.Policies)
	}
	for i, id := range g.Policies {
		if g.Names[i] != want[id] {
			t.Errorf("policy %q is labelled %q, want %q", id, g.Names[i], want[id])
		}
	}
}
