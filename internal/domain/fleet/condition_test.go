package fleet

import "testing"

// A condition is the half of a policy that cannot be enforced, only checked
// (ADR 0017). The two properties that matter are that it compares correctly
// and that an ABSENT metric is unknown rather than a failure - a device this
// fleet does not measure has not broken a rule.

func TestConditionComparisons(t *testing.T) {
	m := map[string]float64{"disk.free_percent": 20}
	for _, tc := range []struct {
		op    string
		value float64
		want  bool
	}{
		{">=", 15, true},
		{">=", 20, true},
		{">=", 25, false},
		{"<=", 25, true},
		{"<", 20, false},
		{">", 19, true},
		{"==", 20, true},
		{"==", 21, false},
	} {
		got, ok := Condition{Metric: "disk.free_percent", Op: tc.op, Value: tc.value}.Holds(m)
		if !ok {
			t.Fatalf("%s %v: reported unknown for a metric that is present", tc.op, tc.value)
		}
		if got != tc.want {
			t.Errorf("20 %s %v = %v, want %v", tc.op, tc.value, got, tc.want)
		}
	}
}

func TestAbsentMetricIsUnknownNotFailure(t *testing.T) {
	_, ok := Condition{Metric: "battery.health", Op: ">=", Value: 80}.Holds(
		map[string]float64{"disk.free_percent": 20})
	if ok {
		t.Fatal("a metric this fleet does not report was judged; unknown must never become an accusation")
	}
}

// An operator the code does not understand is a broken policy. Passing it
// silently would make a policy that checks nothing look like a satisfied one,
// which is the worst possible failure for a compliance artefact.
func TestUnknownOperatorDoesNotSilentlyPass(t *testing.T) {
	holds, ok := Condition{Metric: "disk.free_percent", Op: "=~", Value: 20}.Holds(
		map[string]float64{"disk.free_percent": 20})
	if holds {
		t.Fatal("an unrecognised operator reported the condition as satisfied")
	}
	if ok {
		t.Fatal("an unrecognised operator reported a definite verdict")
	}
}
