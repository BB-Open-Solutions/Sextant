package app

import (
	"context"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// conditionFleet assigns a policy with a condition clause to one group. The
// second group is deliberately left uncovered: a condition must reach exactly
// the devices its assignment targets, and nothing is easier to get wrong than
// a check that quietly applies fleet-wide.
const conditionFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"canary": {}, "fleet": {}},
  "devices": {
    "c-1": {"groups": ["canary"], "hardware": "hw"},
    "f-1": {"groups": ["fleet"], "hardware": "hw"},
    "f-2": {"groups": ["fleet"], "hardware": "hw"}
  },
  "policies": {
    "space": {
      "name": "Room to update",
      "settings": {},
      "conditions": [{
        "metric": "disk.free_percent", "op": ">=", "value": 15,
        "detail": "An update needs room to build and activate."
      }]
    }
  },
  "assignments": [{"policy": "space", "target": "group:canary"}]
}`

func conditionIncidents(t *testing.T, statuses map[string]observed.DeviceStatus) []incident.Incident {
	t.Helper()
	cs, _, _ := newComplianceStackWith(t, &listStatus{m: statuses}, conditionFleet)
	all, err := cs.Incidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var out []incident.Incident
	for _, i := range all {
		if i.Kind == incident.PolicyCondition {
			out = append(out, i)
		}
	}
	return out
}

// A device that reports a figure breaking the condition is a finding, and the
// finding must carry the measurement: "the disk is too full" without a number
// sends the operator off to look it up themselves.
func TestFailedConditionBecomesAFinding(t *testing.T) {
	got := conditionIncidents(t, map[string]observed.DeviceStatus{
		"c-1": {Usage: observed.Usage{DiskUsedGB: 460, DiskTotalGB: 500}}, // 8% free
	})
	if len(got) != 1 {
		t.Fatalf("got %d condition findings, want 1: %+v", len(got), got)
	}
	f := got[0]
	if f.Tag != "c-1" {
		t.Errorf("finding is on %q, want c-1", f.Tag)
	}
	if f.Severity != incident.Warning {
		t.Errorf("severity %v, want Warning: a condition is checked, not enforced", f.Severity)
	}
	if !strings.Contains(f.Title, "Room to update") {
		t.Errorf("title %q does not name the policy; the operator cannot tell which rule they are answering to", f.Title)
	}
	for _, want := range []string{"An update needs room", "disk.free_percent is 8", ">= 15"} {
		if !strings.Contains(f.Detail, want) {
			t.Errorf("detail %q is missing %q", f.Detail, want)
		}
	}
	if !strings.Contains(f.Action, "cannot correct it by converging") {
		t.Errorf("action %q does not say the fleet will not fix this by itself", f.Action)
	}
}

// The satisfied case and the unassigned case must both be silent, and for
// different reasons: one meets the requirement, the other was never asked.
func TestSatisfiedAndUnassignedDevicesAreSilent(t *testing.T) {
	got := conditionIncidents(t, map[string]observed.DeviceStatus{
		"c-1": {Usage: observed.Usage{DiskUsedGB: 100, DiskTotalGB: 500}}, // 80% free
		"f-1": {Usage: observed.Usage{DiskUsedGB: 495, DiskTotalGB: 500}}, // 1% free, no policy
	})
	if len(got) != 0 {
		t.Fatalf("got findings %+v; c-1 meets the condition and f-1 was never assigned it", got)
	}
}

// The one that matters most: a device the fleet cannot measure has not broken
// a rule. An older agent reports no usage at all, and a fleet that accuses it
// of a full disk teaches operators to ignore the whole category.
func TestUnmeasuredDeviceIsNotAccused(t *testing.T) {
	got := conditionIncidents(t, map[string]observed.DeviceStatus{
		"c-1": {Revision: "abc"}, // checked in, reported no usage
	})
	if len(got) != 0 {
		t.Fatalf("a device that reported no metrics was judged: %+v", got)
	}
}

// A device that has never checked in has no status row at all - a different
// path through the service than the one above, and the same requirement.
func TestNeverSeenDeviceIsNotAccused(t *testing.T) {
	if got := conditionIncidents(t, map[string]observed.DeviceStatus{}); len(got) != 0 {
		t.Fatalf("a device that never checked in was judged against a condition: %+v", got)
	}
}
