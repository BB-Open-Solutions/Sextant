package fleet

import (
	"reflect"
	"testing"
)

// nested builds the shape found on the demo fleet: a child group under a
// parent, and devices listed in both. The parent carries settings here even
// though the real one did not, because a migration that only works while the
// ancestor is empty proves nothing.
func nested() *Fleet {
	return &Fleet{
		Version: 3,
		Org:     &Scope{Settings: map[string]any{"desktop": "gnome", "ssh.enable": true}},
		Groups: map[string]Group{
			"gemeente-a": {Settings: map[string]any{"desktop": "plasma", "timesync.enable": true}},
			"zaanstad":   {Parent: "gemeente-a", Settings: map[string]any{"desktop": "kde"}},
			"kantoor":    {Settings: map[string]any{"ssh.enable": false}},
		},
		Devices: map[string]Device{
			// The redundant case: a group and its own parent.
			"lt-1": {Groups: []string{"zaanstad", "gemeente-a"}},
			// The same, listed the other way round. Array order is exactly
			// what this change removes the need to think about, so both
			// orders have to survive it identically.
			"lt-2": {Groups: []string{"gemeente-a", "zaanstad"}},
			// One group only: nothing to do.
			"lt-3": {Groups: []string{"zaanstad"}},
			// Two independent groups: a real choice, left alone.
			"lt-4": {Groups: []string{"zaanstad", "kantoor"}},
		},
	}
}

// The whole claim of this migration is that it changes nothing an operator can
// observe. Asserting that the list got shorter would pass just as happily if
// the wrong entry were dropped, so the assertion is on the resolution.
func TestPruningRedundantGroupsChangesNothingThatResolves(t *testing.T) {
	f := nested()

	before := map[string]map[string]Resolution{}
	for tag := range f.Devices {
		before[tag] = f.Resolve(tag)
	}

	if err := PruneRedundantGroups()(f); err != nil {
		t.Fatal(err)
	}

	for tag := range f.Devices {
		after := f.Resolve(tag)
		if !reflect.DeepEqual(before[tag], after) {
			t.Errorf("%s resolves differently after pruning:\n before %+v\n after  %+v",
				tag, before[tag], after)
		}
	}
}

// And it has to actually do something, in both orders, or the test above is a
// tautology over a no-op.
func TestPruningRemovesTheAncestorAndKeepsTheChild(t *testing.T) {
	f := nested()
	if err := PruneRedundantGroups()(f); err != nil {
		t.Fatal(err)
	}

	for _, tag := range []string{"lt-1", "lt-2"} {
		got := f.Devices[tag].Groups
		if !reflect.DeepEqual(got, []string{"zaanstad"}) {
			t.Errorf("%s = %v, want only the child group; the ancestor is already "+
				"in the chain and listing it twice is what makes array order matter", tag, got)
		}
	}
}

// A device in two groups from unrelated branches is a real decision about
// which one wins. A migration that silently picked one would be making that
// decision on somebody's behalf, in a commit nobody reads as a policy change.
func TestPruningLeavesGenuinelyIndependentGroupsAlone(t *testing.T) {
	f := nested()
	if err := PruneRedundantGroups()(f); err != nil {
		t.Fatal(err)
	}

	got := f.Devices["lt-4"].Groups
	if len(got) != 2 {
		t.Errorf("lt-4 = %v, want both groups kept; choosing between independent "+
			"groups is not a migration's call", got)
	}
	if got := f.Devices["lt-3"].Groups; !reflect.DeepEqual(got, []string{"zaanstad"}) {
		t.Errorf("lt-3 = %v, want it untouched", got)
	}
}

// RedundantGroups is what the console will use to explain the situation before
// anything is changed, so it has to name the entries rather than merely count.
func TestRedundantGroupsNamesWhatIsDuplicated(t *testing.T) {
	f := nested()

	if got := f.RedundantGroups("lt-1"); !reflect.DeepEqual(got, []string{"gemeente-a"}) {
		t.Errorf("lt-1 = %v, want the ancestor named", got)
	}
	if got := f.RedundantGroups("lt-4"); got != nil {
		t.Errorf("lt-4 = %v, want nothing: those two are independent", got)
	}
	if got := f.RedundantGroups("lt-3"); got != nil {
		t.Errorf("lt-3 = %v, want nothing: one group cannot duplicate itself", got)
	}
	if got := f.RedundantGroups("does-not-exist"); got != nil {
		t.Errorf("an unknown device gave %v, want nothing rather than a panic", got)
	}
}
