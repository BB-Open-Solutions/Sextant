package fleet

import "strings"

// chain.go compiles a device's applicable configuration into an ordered list
// of contributors: the inline settings of every scope on the device's chain,
// plus every assigned policy whose target lies on that chain and whose filter
// matches the device. The resolver (resolve.go) then applies one precedence
// rule over the compiled list - policies enrich the model without touching
// the proven resolution math.

// contributor is one source of setting values on a device's chain.
type contributor struct {
	// specificity orders scopes general -> specific: org=0, then each group
	// chain position, device last. Higher is more specific.
	specificity int
	// inline is true for a scope's own settings, false for policy-delivered
	// values. At equal specificity, inline wins: a value set directly on the
	// scope is more explicit than one a policy delivers there.
	inline bool
	// priority is the assignment priority (higher wins among policies at the
	// same scope).
	priority int
	// order is the assignment index, the final deterministic tiebreak.
	order int

	source   Source
	settings map[string]any
	enforced map[string]bool
}

func enforcedSet(list []string) map[string]bool {
	m := make(map[string]bool, len(list))
	for _, k := range list {
		m[k] = true
	}
	return m
}

// scopePositions returns the device's scope chain as ref -> specificity:
// org, then each group's ancestry (root..leaf) in device group order, then
// the device. A scope reached via two group memberships keeps its first
// (most general) position; its settings are identical either way.
//
// When a device belongs to two UNRELATED group hierarchies, this is where
// their relative specificity is decided: by the ORDER the groups appear in
// Device.Groups (first-seen scan), NOT by tree depth. A shallow group listed
// second can outrank a deeper group listed first. This is intentional and
// matches the nix twin (nix/resolve.nix scopePositions) - see
// TestResolve_CrossHierarchyGroupOrderDecidesTies for the pinned behavior.
func (f *Fleet) scopePositions(tag string) map[string]int {
	pos := map[string]int{"org": 0}
	next := 1
	d := f.Devices[tag]
	for _, g := range d.Groups {
		for _, anc := range f.GroupAncestry(g) {
			ref := "group:" + anc
			if _, seen := pos[ref]; !seen {
				pos[ref] = next
				next++
			}
		}
	}
	pos["device"] = next
	return pos
}

// chainFor compiles the full contributor list for a device.
func (f *Fleet) chainFor(tag string) []contributor {
	pos := f.scopePositions(tag)
	d := f.Devices[tag]

	var chain []contributor

	// Inline scope settings.
	if f.Org != nil && (len(f.Org.Settings) > 0 || len(f.Org.Enforced) > 0) {
		chain = append(chain, contributor{
			specificity: pos["org"],
			inline:      true,
			source:      Source{Scope: "org"},
			settings:    f.Org.Settings,
			enforced:    enforcedSet(f.Org.Enforced),
		})
	}
	for ref, p := range pos {
		if !strings.HasPrefix(ref, "group:") {
			continue
		}
		g := f.Groups[strings.TrimPrefix(ref, "group:")]
		if len(g.Settings) == 0 && len(g.Enforced) == 0 {
			continue
		}
		chain = append(chain, contributor{
			specificity: p,
			inline:      true,
			source:      Source{Scope: ref},
			settings:    g.Settings,
			enforced:    enforcedSet(g.Enforced),
		})
	}
	if len(d.Settings) > 0 || len(d.Enforced) > 0 {
		chain = append(chain, contributor{
			specificity: pos["device"],
			inline:      true,
			source:      Source{Scope: "device"},
			settings:    d.Settings,
			enforced:    enforcedSet(d.Enforced),
		})
	}

	// Policy contributions via assignments.
	for i, a := range f.Assignments {
		p, ok := f.Policies[a.Policy]
		if !ok {
			continue // dangling assignment; rejected at write time
		}
		spec, applies := assignmentPosition(pos, a.Target, tag)
		if !applies {
			continue
		}
		if a.Filter != "" {
			fl, ok := f.Filters[a.Filter]
			if !ok || !f.matchesFilter(fl, tag) {
				continue // missing filter fails closed
			}
		}
		chain = append(chain, contributor{
			specificity: spec,
			inline:      false,
			priority:    a.Priority,
			order:       i,
			source:      Source{Scope: displayScope(a.Target), Policy: a.Policy},
			settings:    p.Settings,
			enforced:    enforcedSet(p.Enforced),
		})
	}
	return chain
}

// assignmentPosition maps an assignment target onto the device's chain.
// Returns (specificity, true) when the target scope applies to this device.
func assignmentPosition(pos map[string]int, target, tag string) (int, bool) {
	switch {
	case target == "org":
		return pos["org"], true
	case strings.HasPrefix(target, "group:"):
		p, ok := pos[target]
		return p, ok
	case strings.HasPrefix(target, "device:"):
		if strings.TrimPrefix(target, "device:") == tag {
			return pos["device"], true
		}
	}
	return 0, false
}

// displayScope renders an assignment target as a provenance scope ref.
func displayScope(target string) string {
	if strings.HasPrefix(target, "device:") {
		return "device"
	}
	return target
}
