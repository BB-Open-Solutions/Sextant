package fleet

import "testing"

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
