package fleet

import "testing"

// Auto-flow (ADR 0012) is on unless the org says otherwise, so an unset
// field must read as enabled - only an explicit false is manual dispatch.
func TestRolloutAutoFlowEnabled(t *testing.T) {
	on, off := true, false
	cases := []struct {
		name string
		pol  *RolloutPolicy
		want bool
	}{
		{"no plan at all", nil, false},
		{"unset means on", &RolloutPolicy{Rings: []RolloutRing{{Group: "a"}}}, true},
		{"explicit true", &RolloutPolicy{Rings: []RolloutRing{{Group: "a"}}, AutoFlow: &on}, true},
		{"explicit false", &RolloutPolicy{Rings: []RolloutRing{{Group: "a"}}, AutoFlow: &off}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.pol.AutoFlowEnabled(); got != tc.want {
				t.Fatalf("AutoFlowEnabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestRolloutHasTestGate(t *testing.T) {
	var nilPol *RolloutPolicy
	if nilPol.HasTestGate() {
		t.Fatal("nil policy should have no test gate")
	}
	noGate := &RolloutPolicy{Rings: []RolloutRing{{Group: "a"}, {Group: "b"}}}
	if noGate.HasTestGate() {
		t.Fatal("plan without an approval ring should have no test gate")
	}
	gated := &RolloutPolicy{Rings: []RolloutRing{{Group: "test", RequireApproval: true}, {Group: "b"}}}
	if !gated.HasTestGate() {
		t.Fatal("plan with an approval ring should have a test gate")
	}
}
