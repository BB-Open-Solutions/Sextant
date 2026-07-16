package fleet

// classes.go: equivalence classes over the fleet's devices - the partitioner
// behind the interactive gate's sampling (docs/architecture/scale.md).
//
// Two devices are equivalent when the generator would build them from the
// same inputs apart from their identity (tag/hostname): same hardware
// profile, same class, same EFFECTIVE settings (the resolver output, so
// policies, assignments, filters, enforce and inheritance are all baked in)
// and same app sets. An option/type/assertion error in a change then fails
// every member of a class identically, so evaluating one representative per
// class proves the change against every distinct configuration shape.
//
// SECURITY NOTE: this partitioner narrows what the interactive gate
// evaluates. It must never place two devices with differing build inputs in
// the same class - that would let an error on the unsampled device through
// the interactive check. It keys therefore on the RESOLVED state (never on
// raw scope data, which could miss a filter or policy) and errs toward more
// classes, never fewer. The full per-host proof still happens down the
// pipeline: a ring's release realises every member's toplevel before the
// ring branch moves, and a device's own nixos-rebuild converges
// generation-safe regardless.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// EquivalenceClasses partitions the ACTIVE devices into configuration-shape
// classes: class key -> sorted member tags. Retired devices do not build and
// are excluded.
func (f *Fleet) EquivalenceClasses() map[string][]string {
	classes := map[string][]string{}
	for tag, d := range f.Devices {
		if d.Retired() {
			continue
		}
		key := f.classKey(tag, d)
		classes[key] = append(classes[key], tag)
	}
	for _, members := range classes {
		sort.Strings(members)
	}
	return classes
}

// Representatives returns one device per equivalence class - the sorted
// first member, so the choice is deterministic across calls and processes -
// as a sorted host list for the gate.
func (f *Fleet) Representatives() []string {
	classes := f.EquivalenceClasses()
	reps := make([]string, 0, len(classes))
	for _, members := range classes {
		reps = append(reps, members[0])
	}
	sort.Strings(reps)
	return reps
}

// SampleHosts narrows a SCOPED blast radius to one representative per
// configuration shape among the given tags: a group re-parent that touches N
// subtree devices only needs to evaluate each DISTINCT shape once, since an
// option/type error fails every device of that shape identically. Same
// sampling contract and residual as Representatives (the full per-host proof
// is the ring build); the difference is the domain - here it is a caller-
// supplied subset, not the whole fleet. Unknown/retired tags drop out. An
// empty or single-tag input is returned unchanged.
func (f *Fleet) SampleHosts(tags []string) []string {
	if len(tags) <= 1 {
		return tags
	}
	firstOfClass := map[string]string{}
	for _, tag := range tags {
		d, ok := f.Devices[tag]
		if !ok || d.Retired() {
			continue
		}
		key := f.classKey(tag, d)
		if cur, seen := firstOfClass[key]; !seen || tag < cur {
			firstOfClass[key] = tag
		}
	}
	out := make([]string, 0, len(firstOfClass))
	for _, tag := range firstOfClass {
		out = append(out, tag)
	}
	sort.Strings(out)
	return out
}

// classKey derives a device's configuration-shape fingerprint from its
// resolved (effective) state.
func (f *Fleet) classKey(tag string, d Device) string {
	var b strings.Builder

	// Identity-adjacent build inputs.
	fmt.Fprintf(&b, "hw=%s\nclass=%s\n", d.Hardware, d.Class)

	// Group membership (expanded ancestry) and pins: the generator derives
	// the comin branch a device follows from these (ringBranchFor), and a
	// group may carry generator-visible attributes beyond settings. Keying
	// on them errs toward more classes - the safe direction.
	fmt.Fprintf(&b, "pin=%s\n", d.Pin)
	ancestry := map[string]bool{}
	for _, g := range d.Groups {
		for _, a := range f.GroupAncestry(g) {
			ancestry[a] = true
		}
	}
	groups := make([]string, 0, len(ancestry))
	for g := range ancestry {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		fmt.Fprintf(&b, "g:%s;pin=%s\n", g, f.Groups[g].Pin)
	}

	// Effective settings: resolver output, key-sorted, values in canonical
	// JSON so 1 and "1" (or differently-ordered maps) never collide.
	// Enforced-ness is part of the shape: it decides mkForce vs mkDefault in
	// the generated module.
	for _, rs := range f.ResolveSorted(tag) {
		val, err := json.Marshal(rs.Value)
		if err != nil {
			// Unmarshalable settings cannot come out of fleet.json (it was
			// JSON to begin with); if one ever does, isolate the device in
			// its own class rather than risk grouping it wrongly.
			val = []byte(fmt.Sprintf("!unhashable:%s:%v", tag, rs.Value))
		}
		fmt.Fprintf(&b, "s:%s=%s;e=%t\n", rs.Key, val, rs.Enforced)
	}

	// Effective app sets (additive across the chain). Both the sets and the
	// section order are canonicalised - a fingerprint must never depend on
	// map iteration order.
	pkgs, flats, ovs := f.ResolveApps(tag)
	for _, sec := range []struct {
		prefix string
		list   []string
	}{{"p", pkgs}, {"f", flats}, {"o", ovs}} {
		s := append([]string(nil), sec.list...)
		sort.Strings(s)
		fmt.Fprintf(&b, "%s:%s\n", sec.prefix, strings.Join(s, ","))
	}

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:16])
}
