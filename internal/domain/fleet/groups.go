package fleet

import (
	"fmt"
	"slices"
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

// classAllowedBy reports whether a device of the given class may be a member of
// group g. An empty AllowedClasses guardrail permits every class.
func classAllowedBy(g Group, class string) bool {
	if len(g.AllowedClasses) == 0 {
		return true
	}
	return slices.Contains(g.AllowedClasses, class)
}

// checkClassAllowed verifies a device's class passes the AllowedClasses
// guardrail of every named group it would join. Unknown group names are left
// for the caller's own existence check; this only enforces the class gate.
func (f *Fleet) checkClassAllowed(class string, groups []string) error {
	for _, name := range groups {
		g, ok := f.Groups[name]
		if !ok {
			continue
		}
		if !classAllowedBy(g, class) {
			return fmt.Errorf("device class %q is not allowed in group %q (allowed: %s)",
				class, name, strings.Join(g.AllowedClasses, ", "))
		}
	}
	return nil
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

// rolloutRing returns the rollout wave whose group is `group`, or nil.
func (f *Fleet) rolloutRing(group string) *RolloutRing {
	if f.Rollout == nil {
		return nil
	}
	for i := range f.Rollout.Rings {
		if f.Rollout.Rings[i].Group == group {
			return &f.Rollout.Rings[i]
		}
	}
	return nil
}

// ReleasedGroupDevices is the set of a wave's group devices that have received
// the target so far (ADR 0013). An uncapped wave releases the whole active
// group at once; a capped wave releases only devices pinned to the ring (the
// cohort the engine has marked). Order is deterministic (GroupDevices sorts).
func (f *Fleet) ReleasedGroupDevices(group string) []string {
	active := f.ActiveGroupDevices(group)
	if ring := f.rolloutRing(group); ring == nil || ring.MaxDevices <= 0 {
		return active // uncapped: the whole group is released
	}
	out := make([]string, 0, len(active))
	for _, tag := range active {
		if f.Devices[tag].Pin == group {
			out = append(out, tag)
		}
	}
	return out
}

// TargetDevices resolves an assignment/scope target to its ACTIVE devices:
// "org" is the whole active fleet, "group:<g>" the group's subtree,
// "device:<t>" that device (when active). Unknown shapes resolve to none.
func (f *Fleet) TargetDevices(target string) []string {
	switch {
	case target == "org":
		var tags []string
		for _, tag := range f.DeviceTags() {
			if !f.Devices[tag].Retired() {
				tags = append(tags, tag)
			}
		}
		return tags
	case strings.HasPrefix(target, "group:"):
		return f.ActiveGroupDevices(strings.TrimPrefix(target, "group:"))
	case strings.HasPrefix(target, "device:"):
		tag := strings.TrimPrefix(target, "device:")
		if d, ok := f.Devices[tag]; ok && !d.Retired() {
			return []string{tag}
		}
	}
	return nil
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
