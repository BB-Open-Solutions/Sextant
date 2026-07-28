package web

import (
	"slices"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// device_policies.go: the device page's "applied policies" panel - the
// Intune device-configuration idiom. One row per assignment that actually
// reaches this device: which policy, via which target/filter, how many
// settings (and locks) it contributes, and its profile state so drift is
// visible where the operator is looking (the device), not only on the
// policy-centric policies page.

// devicePolicyRow is one policy application on one device.
type devicePolicyRow struct {
	ID       string
	Target   string // assignment target ref ("org", "group:x", "device:y")
	Filter   string // narrowing filter name, "" when none
	Settings int    // keys the policy contributes
	Enforced int    // of which locks
	// State mirrors the policies page: "current", "reapply" (drift),
	// "edited", or "" for a hand-made policy without profile provenance.
	State string
}

// devicePolicyRows lists the assignments reaching one device, sorted by
// policy id then target for stable rendering.
func devicePolicyRows(f *fleet.Fleet, profiles *fleet.Profiles, tag string) []devicePolicyRow {
	var out []devicePolicyRow
	for _, a := range f.Assignments {
		if !slices.Contains(f.AssignmentDevices(a), tag) {
			continue
		}
		p, ok := f.Policies[a.Policy]
		if !ok {
			continue
		}
		row := devicePolicyRow{ID: a.Policy, Target: a.Target, Filter: a.Filter,
			Settings: len(p.Settings), Enforced: len(p.Enforced)}
		if name, _, ok := strings.Cut(p.Profile, "@"); ok {
			if src, has := profiles.Get(name); has {
				row.State = profileState(p, src)
			}
		}
		out = append(out, row)
	}
	slices.SortFunc(out, func(a, b devicePolicyRow) int {
		if a.ID != b.ID {
			return strings.Compare(a.ID, b.ID)
		}
		return strings.Compare(a.Target, b.Target)
	})
	return out
}
