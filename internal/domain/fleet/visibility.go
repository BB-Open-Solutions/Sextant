package fleet

// visibility.go narrows the fleet document to what one principal may READ.
// Writes were always per-scope; reads used to expose the whole document to
// any authenticated viewer. Per-scope read-confidentiality: a viewer bound
// to group A must not learn group B's existence, settings, devices or
// bindings.
//
// The transport builds canView from the identity resolver (RoleAt >= Viewer,
// after any token ceiling) against the FULL document; the filtered copy is
// for rendering and API output only, never for authorization decisions.

// VisibleTo returns the document narrowed to the scopes canView allows.
// The result shares immutable sub-values with the receiver; treat it as
// read-only. Rules:
//
//   - org viewer: the full document, unfiltered.
//   - org root: always kept - its settings govern the caller's own devices
//     and are needed to explain any resolved value.
//   - groups/devices: kept only when the caller may view that scope
//     (a binding at a group covers its subtree, so ancestors and siblings
//     of the bound group drop out).
//   - assignments: kept when org-targeted (they govern the caller's devices
//     too) or when their target scope is visible.
//   - policies and filters: kept only while referenced by a kept assignment;
//     an unreferenced bundle may describe another department.
//   - access bindings: org-wide ones plus those at visible scopes.
//   - rollout plan: org-level machinery; it enumerates groups, so non-org
//     viewers do not see it.
func (f *Fleet) VisibleTo(canView func(ref string) bool) *Fleet {
	if canView("org") {
		return f
	}
	out := &Fleet{
		Version:   f.Version,
		Org:       f.Org,
		Assurance: f.Assurance,
		Groups:    map[string]Group{},
		Devices:   map[string]Device{},
	}
	for name, g := range f.Groups {
		if canView("group:" + name) {
			out.Groups[name] = g
		}
	}
	for tag, d := range f.Devices {
		if canView("device:" + tag) {
			out.Devices[tag] = d
		}
	}
	keepPolicy := map[string]bool{}
	// A filter referenced by an org-targeted assignment must stay
	// enumerable (the assignment governs the caller's own devices too, so
	// hiding it entirely would break rendering), but its Rules can name a
	// scope the caller cannot see (a group, a user, a label) - keepFilterFull
	// tracks filters reachable through a scope the caller CAN see (safe to
	// expose whole); keepFilterOrgOnly tracks filters reachable only through
	// an org-targeted assignment (Rules must be redacted below).
	keepFilterFull := map[string]bool{}
	keepFilterOrgOnly := map[string]bool{}
	for _, a := range f.Assignments {
		if a.Target != "org" && !canView(a.Target) {
			continue
		}
		out.Assignments = append(out.Assignments, a)
		keepPolicy[a.Policy] = true
		if a.Filter == "" {
			continue
		}
		if a.Target == "org" {
			keepFilterOrgOnly[a.Filter] = true
		} else {
			keepFilterFull[a.Filter] = true
		}
	}
	if len(keepPolicy) > 0 {
		out.Policies = map[string]Policy{}
		for id, p := range f.Policies {
			if keepPolicy[id] {
				out.Policies[id] = p
			}
		}
	}
	if len(keepFilterFull) > 0 || len(keepFilterOrgOnly) > 0 {
		out.Filters = map[string]Filter{}
		for id, fl := range f.Filters {
			switch {
			case keepFilterFull[id]:
				out.Filters[id] = fl
			case keepFilterOrgOnly[id]:
				// Redact Rules: VisibleTo output is render-only, never an
				// authorization decision, so a caller only ever learns that
				// an org-scoped filter with this name/match kind exists -
				// never which group/user/label it names.
				out.Filters[id] = Filter{Name: fl.Name, Match: fl.Match}
			}
		}
	}
	for _, b := range f.Access {
		if b.Scope == "org" || canView(b.Scope) {
			out.Access = append(out.Access, b)
		}
	}
	return out
}
