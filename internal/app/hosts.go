package app

import (
	"slices"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// AffectedHosts computes the blast radius of a change at a scope ref: the
// device tags whose configuration can change, used to scope the gate.
// nil means "validate everything" (org-wide or unknown blast radius).
func AffectedHosts(f *fleet.Fleet, ref string) []string {
	switch {
	case strings.HasPrefix(ref, "device:"):
		tag := strings.TrimPrefix(ref, "device:")
		// A retired device has no host attribute in the generator; gating
		// it by name would fail on a missing attr. Fall back to org-wide.
		if d, ok := f.Devices[tag]; ok && d.Retired() {
			return nil
		}
		return []string{tag}
	case strings.HasPrefix(ref, "group:"):
		g := strings.TrimPrefix(ref, "group:")
		// A group change flows to its whole subtree: every active device
		// with g in its expanded ancestry (retired devices do not build).
		var tags []string
		for tag, d := range f.Devices {
			if d.Retired() {
				continue
			}
			for _, dg := range d.Groups {
				if slices.Contains(f.GroupAncestry(dg), g) {
					tags = append(tags, tag)
					break
				}
			}
		}
		slices.Sort(tags)
		return tags
	}
	return nil
}
