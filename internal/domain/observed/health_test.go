package observed

import "testing"

// Degraded is the veto the console leans on, so what counts and what does not
// is asserted rather than inferred from the field names.
func TestDegradedOnlyOnAReportedFailure(t *testing.T) {
	cases := []struct {
		name string
		h    Health
		want bool
	}{
		// Silence is not health, but it is not an accusation either: an older
		// agent, or a probe that could not run, reports nothing and must be
		// judged on its other signals.
		{"nothing reported", Health{}, false},
		{"healthy machine", Health{State: "running"}, false},
		{"systemd says degraded", Health{State: "degraded"}, true},
		// The unit list is authoritative on its own: a machine can carry a
		// failed unit while systemd still calls itself running (a unit that
		// failed after startup completed).
		{"units failed", Health{State: "running", FailedUnits: []string{"sssd.service"}}, true},
		{"starting up is not broken", Health{State: "starting"}, false},
	}
	for _, c := range cases {
		if got := c.h.Degraded(); got != c.want {
			t.Errorf("%s: Degraded() = %v, want %v", c.name, got, c.want)
		}
	}
}
