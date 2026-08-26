package fleet

import (
	"strings"
	"testing"
)

// The console has two ways to change membership - editing a device and adding
// a device to a group - and both have to refuse the same thing. A guard on one
// path only is worse than none, because it teaches an operator the rule exists
// and then lets the other route through.

func guarded() *Fleet {
	return &Fleet{
		Version: 3,
		Org:     &Scope{Settings: map[string]any{}},
		Groups: map[string]Group{
			"gemeente-a": {},
			"zaanstad":   {Parent: "gemeente-a"},
			"kantoor":    {},
		},
		Devices: map[string]Device{"lt-1": {Groups: []string{"zaanstad"}}},
	}
}

func TestEditingADeviceCannotAddItsOwnAncestor(t *testing.T) {
	f := guarded()
	groups := []string{"zaanstad", "gemeente-a"}

	err := UpdateDevice("lt-1", DevicePatch{Groups: &groups})(f)
	if err == nil {
		t.Fatal("adding a group's own parent was accepted; that is the duplication " +
			"that makes array order decide ties")
	}
	// The message has to name both, since an operator editing a device does not
	// see the hierarchy from there.
	for _, want := range []string{"gemeente-a", "zaanstad"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %q", err, want)
		}
	}
	if got := f.Devices["lt-1"].Groups; len(got) != 1 {
		t.Errorf("the device was changed anyway: %v", got)
	}
}

func TestAddingADeviceToAGroupCannotDuplicateAnAncestor(t *testing.T) {
	f := guarded()

	if err := CreateGroupWithDevices("nieuw", Group{Parent: "zaanstad"}, []string{"lt-1"})(f); err == nil {
		t.Fatal("the group route accepted a child of a group the device is already in")
	}
	if got := f.Devices["lt-1"].Groups; len(got) != 1 {
		t.Errorf("membership changed despite the refusal: %v", got)
	}
}

// The guard must not overreach. Two groups from unrelated branches remain a
// legitimate thing to write until the model itself changes (#115), and a
// device that is simply moved to another group has nothing to do with
// ancestry at all.
func TestTheGuardLeavesLegitimateMembershipAlone(t *testing.T) {
	f := guarded()

	both := []string{"zaanstad", "kantoor"}
	if err := UpdateDevice("lt-1", DevicePatch{Groups: &both})(f); err != nil {
		t.Errorf("two independent groups were refused: %v", err)
	}

	one := []string{"kantoor"}
	if err := UpdateDevice("lt-1", DevicePatch{Groups: &one})(f); err != nil {
		t.Errorf("moving to a single group was refused: %v", err)
	}
	if got := f.Devices["lt-1"].Groups; len(got) != 1 || got[0] != "kantoor" {
		t.Errorf("groups = %v, want the move to have happened", got)
	}
}
