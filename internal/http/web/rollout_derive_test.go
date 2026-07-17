package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

func TestParsePercents(t *testing.T) {
	cases := []struct {
		in      string
		want    []int
		wantErr bool
	}{
		{"", []int{100}, false},
		{"10, 30, 60", []int{10, 30, 60}, false},
		{"10-20-30-40", []int{10, 20, 30, 40}, false},
		{"0, 100", nil, true},
		{"tien", nil, true},
		{"50, 40", nil, true},                 // sums to 90, not 100
		{"10, 10, 20, 20, 20, 20", nil, true}, // six waves
		{"100", []int{100}, false},
	}
	for _, c := range cases {
		got, err := parsePercents(c.in)
		if (err != nil) != c.wantErr {
			t.Errorf("parsePercents(%q) err = %v, wantErr %v", c.in, err, c.wantErr)
			continue
		}
		if err != nil {
			continue
		}
		if len(got) != len(c.want) {
			t.Errorf("parsePercents(%q) = %v, want %v", c.in, got, c.want)
			continue
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Errorf("parsePercents(%q) = %v, want %v", c.in, got, c.want)
				break
			}
		}
	}
}

// ladderFleet builds a fleet whose group sizes exercise the bin-packing: a
// test group plus four groups of 1, 2, 3 and 4 devices (10 in the ladder).
func ladderFleet(t *testing.T) *fleet.Fleet {
	t.Helper()
	f := &fleet.Fleet{
		Version: fleet.Version,
		Groups: map[string]fleet.Group{
			"test": {}, "tiny": {}, "small": {}, "mid": {}, "big": {},
		},
		Devices: map[string]fleet.Device{},
	}
	add := func(group string, n int) {
		for i := 0; i < n; i++ {
			tag := group + "-" + string(rune('a'+i))
			f.Devices[tag] = fleet.Device{Groups: []string{group}}
		}
	}
	add("test", 2)
	add("tiny", 1)
	add("small", 2)
	add("mid", 3)
	add("big", 4)
	return f
}

func TestDerivePlanPercentageWaves(t *testing.T) {
	f := ladderFleet(t)
	plan := derivePlan(f, "test", []int{10, 30, 60})

	if plan.Rings[0].Group != "test" || !plan.Rings[0].RequireApproval {
		t.Fatalf("ring 0 must be the gated test wave, got %+v", plan.Rings[0])
	}
	// 10 ladder devices: cumulative targets 1, 4, 10 -> tiny | small(+overshoot
	// already covered) ... groups whole, smallest first.
	gotGroups := make([][]string, 0, len(plan.Rings)-1)
	for _, r := range plan.Rings[1:] {
		gotGroups = append(gotGroups, r.GroupList())
	}
	if len(gotGroups) != 3 {
		t.Fatalf("want 3 percentage waves, got %d (%v)", len(gotGroups), gotGroups)
	}
	seen := map[string]bool{}
	total := 0
	for _, gs := range gotGroups {
		for _, g := range gs {
			if seen[g] {
				t.Errorf("group %s in two waves", g)
			}
			seen[g] = true
			total += len(f.ActiveGroupDevices(g))
		}
	}
	if total != 10 {
		t.Errorf("waves cover %d of 10 ladder devices", total)
	}
	if gotGroups[0][0] != "tiny" {
		t.Errorf("first wave should start with the smallest group, got %v", gotGroups[0])
	}
	// The plan must pass its own validation (groups exist, no duplicates).
	if err := fleet.SetRolloutPlan(plan)(f); err != nil {
		t.Errorf("derived plan rejected by SetRolloutPlan: %v", err)
	}
}

func TestDerivePlanFewGroupsCollapsesWaves(t *testing.T) {
	f := &fleet.Fleet{
		Version: fleet.Version,
		Groups:  map[string]fleet.Group{"test": {}, "only": {}},
		Devices: map[string]fleet.Device{
			"t-a": {Groups: []string{"test"}},
			"o-a": {Groups: []string{"only"}},
		},
	}
	plan := derivePlan(f, "test", []int{10, 30, 60})
	if len(plan.Rings) != 2 {
		t.Fatalf("one ladder group must collapse to one wave, got %d rings", len(plan.Rings))
	}
	if got := plan.Rings[1].GroupList(); len(got) != 1 || got[0] != "only" {
		t.Errorf("wave 1 = %v, want [only]", got)
	}
}
