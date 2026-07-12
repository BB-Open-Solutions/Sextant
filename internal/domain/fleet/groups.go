package fleet

import (
	"fmt"
	"sort"
	"strings"
)

// GroupAncestry returns g's chain from the root ancestor down to g itself
// (general -> specific), e.g. [root, mid, g]. A broken or cyclic parent link
// is cut: traversal stops at the first missing or already-seen parent, so the
// result is always finite and starts at the highest resolvable ancestor.
// Ported from the proven PoC implementation.
func (f *Fleet) GroupAncestry(g string) []string {
	var chain []string
	seen := map[string]bool{}
	for cur := g; cur != ""; {
		if seen[cur] {
			break // cycle guard
		}
		seen[cur] = true
		grp, ok := f.Groups[cur]
		if !ok {
			break
		}
		chain = append([]string{cur}, chain...) // prepend: build root-first
		cur = grp.Parent
	}
	return chain
}

// GroupChildren returns the direct children of a group, sorted. Passing ""
// returns the root groups; a dangling parent counts as root.
func (f *Fleet) GroupChildren(parent string) []string {
	var out []string
	for name, g := range f.Groups {
		p := g.Parent
		if _, ok := f.Groups[p]; p != "" && !ok {
			p = ""
		}
		if p == parent {
			out = append(out, name)
		}
	}
	sort.Strings(out)
	return out
}

// GroupDevices returns the tags of the devices that are members of group
// (directly, not via subgroups), sorted.
func (f *Fleet) GroupDevices(group string) []string {
	var tags []string
	for tag, d := range f.Devices {
		for _, g := range d.Groups {
			if g == group {
				tags = append(tags, tag)
				break
			}
		}
	}
	sort.Strings(tags)
	return tags
}

// Retired reports whether the device is parked (audit record only).
func (d Device) Retired() bool { return d.State == DeviceRetired }

// ActiveGroupDevices is GroupDevices minus retired devices: the set that
// builds images, checks in and counts for rollout convergence.
func (f *Fleet) ActiveGroupDevices(group string) []string {
	var tags []string
	for _, tag := range f.GroupDevices(group) {
		if !f.Devices[tag].Retired() {
			tags = append(tags, tag)
		}
	}
	return tags
}

// DeviceTags returns all device asset tags, sorted.
func (f *Fleet) DeviceTags() []string {
	ks := make([]string, 0, len(f.Devices))
	for k := range f.Devices {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}

// AcceptanceFor reports whether a control key has a documented risk
// acceptance for a device, searching most-specific to most-general (device,
// group ancestry child-before-parent, org). Comply-or-explain: a failing
// control with an acceptance is explained, not open.
func (f *Fleet) AcceptanceFor(tag, key string) (reason, scope string, ok bool) {
	d, exists := f.Devices[tag]
	if exists {
		if r := d.Accepted[key]; r != "" {
			return r, "device", true
		}
		for i := len(d.Groups) - 1; i >= 0; i-- {
			anc := f.GroupAncestry(d.Groups[i])
			for j := len(anc) - 1; j >= 0; j-- {
				if r := f.Groups[anc[j]].Accepted[key]; r != "" {
					return r, "group:" + anc[j], true
				}
			}
		}
	}
	if f.Org != nil {
		if r := f.Org.Accepted[key]; r != "" {
			return r, "org", true
		}
	}
	return "", "", false
}

// AcceptancesAt returns the risk-acceptance register recorded directly at one
// scope (org / group:x / device:y): control key -> justification. Unknown
// scopes error like the mutators do.
func (f *Fleet) AcceptancesAt(ref string) (map[string]string, error) {
	switch {
	case ref == "org":
		if f.Org == nil {
			return nil, nil
		}
		return f.Org.Accepted, nil
	case strings.HasPrefix(ref, "group:"):
		g, ok := f.Groups[strings.TrimPrefix(ref, "group:")]
		if !ok {
			return nil, fmt.Errorf("unknown group %q", strings.TrimPrefix(ref, "group:"))
		}
		return g.Accepted, nil
	case strings.HasPrefix(ref, "device:"):
		d, ok := f.Devices[strings.TrimPrefix(ref, "device:")]
		if !ok {
			return nil, fmt.Errorf("unknown device %q", strings.TrimPrefix(ref, "device:"))
		}
		return d.Accepted, nil
	}
	return nil, fmt.Errorf("bad scope %q", ref)
}
