package fleet

import (
	"fmt"
	"slices"
	"strings"
)

// policy.go: mutations for policies, assignments and filters. Referential
// integrity is enforced at write time so resolution never sees a dangling
// reference: a policy in use cannot be deleted, an assignment must name an
// existing policy/filter/target, a filter in use cannot be deleted.

// PutPolicy creates or replaces a policy. Every enforced key must be one of
// the policy's own setting keys.
func PutPolicy(id string, p Policy) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(id) {
			return fmt.Errorf("policy id %q: must be a lowercase slug", id)
		}
		// A policy must actually say something. Settings OR conditions: a
		// policy that only requires "disk above 15%" is a legitimate policy
		// with no settings at all (ADR 0017), so the old "no settings" check
		// would have refused exactly the kind of clause conditions exist for.
		if len(p.Settings) == 0 && len(p.Conditions) == 0 {
			return fmt.Errorf("policy %q says nothing: it needs settings, conditions, or both", id)
		}
		for i, c := range p.Conditions {
			if err := c.Valid(); err != nil {
				return fmt.Errorf("policy %q condition %d: %w", id, i+1, err)
			}
		}
		for _, k := range p.Enforced {
			if _, has := p.Settings[k]; !has {
				return fmt.Errorf("policy %q enforces %q but does not set it", id, k)
			}
		}
		if f.Policies == nil {
			f.Policies = map[string]Policy{}
		}
		f.Policies[id] = p
		return nil
	}
}

// AssignmentDevices returns the active devices an assignment currently
// reaches (target scope plus filter), sorted. This is the editor's
// reality check: an assignment whose class filter excludes its whole
// target is a silent no-op unless the count makes it visible.
func (f *Fleet) AssignmentDevices(a Assignment) []string {
	var out []string
	for tag, d := range f.Devices {
		if d.State == DeviceRetired {
			continue
		}
		pos := f.scopePositions(tag)
		if _, applies := assignmentPosition(pos, a.Target, tag); !applies {
			continue
		}
		if a.Filter != "" {
			fl, ok := f.Filters[a.Filter]
			if !ok || !f.matchesFilter(fl, tag) {
				continue
			}
		}
		out = append(out, tag)
	}
	slices.Sort(out)
	return out
}

// DeletePolicy removes a policy; refused while any assignment references it.
func DeletePolicy(id string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Policies[id]; !ok {
			return fmt.Errorf("unknown policy %q", id)
		}
		for _, a := range f.Assignments {
			if a.Policy == id {
				return fmt.Errorf("policy %q is assigned to %s; unassign first", id, a.Target)
			}
		}
		delete(f.Policies, id)
		return nil
	}
}

// Assign binds a policy to a scope target. The policy, target scope and
// filter (when named) must exist. Duplicate (policy, target, filter) triples
// are rejected.
func Assign(a Assignment) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Policies[a.Policy]; !ok {
			return fmt.Errorf("unknown policy %q", a.Policy)
		}
		if err := f.validateTarget(a.Target); err != nil {
			return err
		}
		if a.Filter != "" {
			if _, ok := f.Filters[a.Filter]; !ok {
				return fmt.Errorf("unknown filter %q", a.Filter)
			}
		}
		for _, ex := range f.Assignments {
			if ex.Policy == a.Policy && ex.Target == a.Target && ex.Filter == a.Filter {
				return fmt.Errorf("policy %q is already assigned to %s", a.Policy, a.Target)
			}
		}
		f.Assignments = append(f.Assignments, a)
		return nil
	}
}

// Unassign removes the assignment matching (policy, target, filter).
func Unassign(policy, target, filter string) Mutation {
	return func(f *Fleet) error {
		before := len(f.Assignments)
		f.Assignments = slices.DeleteFunc(f.Assignments, func(a Assignment) bool {
			return a.Policy == policy && a.Target == target && a.Filter == filter
		})
		if len(f.Assignments) == before {
			return fmt.Errorf("no assignment of %q to %s", policy, target)
		}
		return nil
	}
}

// PutFilter creates or replaces a filter after validating its rules.
func PutFilter(id string, fl Filter) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(id) {
			return fmt.Errorf("filter id %q: must be a lowercase slug", id)
		}
		if err := ValidateFilter(fl); err != nil {
			return fmt.Errorf("filter %q: %w", id, err)
		}
		// Exact group rules (eq/in) must name real groups: groups are
		// first-class (unlike class/hardware values, which may legitimately
		// precede the devices that carry them), so a typo here is always a
		// mistake that would silently match nothing. ne/prefix values are
		// patterns, not names - they stay free. Resolution itself still
		// fails closed on unknown groups: a document edited outside the
		// console keeps its semantics, this only guards console writes.
		for _, r := range fl.Rules {
			if r.Attr != AttrGroup || (r.Op != OpEq && r.Op != OpIn) {
				continue
			}
			for _, g := range append([]string{r.Value}, r.Values...) {
				if g == "" {
					continue
				}
				if _, ok := f.Groups[g]; !ok {
					return fmt.Errorf("filter %q: unknown group %q", id, g)
				}
			}
		}
		if f.Filters == nil {
			f.Filters = map[string]Filter{}
		}
		f.Filters[id] = fl
		return nil
	}
}

// DeleteFilter removes a filter; refused while any assignment references it.
func DeleteFilter(id string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Filters[id]; !ok {
			return fmt.Errorf("unknown filter %q", id)
		}
		for _, a := range f.Assignments {
			if a.Filter == id {
				return fmt.Errorf("filter %q is used by an assignment of %q; unassign first", id, a.Policy)
			}
		}
		delete(f.Filters, id)
		return nil
	}
}

// validateTarget checks that a scope ref points at an existing scope.
func (f *Fleet) validateTarget(ref string) error {
	switch {
	case ref == "org":
		return nil
	case strings.HasPrefix(ref, "group:"):
		if _, ok := f.Groups[strings.TrimPrefix(ref, "group:")]; !ok {
			return fmt.Errorf("unknown group in target %q", ref)
		}
		return nil
	case strings.HasPrefix(ref, "device:"):
		if _, ok := f.Devices[strings.TrimPrefix(ref, "device:")]; !ok {
			return fmt.Errorf("unknown device in target %q", ref)
		}
		return nil
	}
	return fmt.Errorf("bad target %q (want org|group:<name>|device:<tag>)", ref)
}

// ConditionsFor returns the condition clauses that apply to a device, tagged
// with the policy that asked for them, in a stable order.
//
// Conditions are the half of a policy that cannot be enforced, only checked
// (ADR 0017), so this is the input to a finding rather than to convergence.
// The policy id travels with the condition because a bare "disk below 15%" is
// not actionable: the operator needs to know which rule they are answering to,
// and an auditor needs the trail back to the intent.
func (f *Fleet) ConditionsFor(tag string) []PolicyCondition {
	var out []PolicyCondition
	for _, a := range f.Assignments {
		p, ok := f.Policies[a.Policy]
		if !ok || len(p.Conditions) == 0 {
			continue
		}
		if !slices.Contains(f.AssignmentDevices(a), tag) {
			continue
		}
		for _, c := range p.Conditions {
			out = append(out, PolicyCondition{Policy: a.Policy, Name: p.Name, Condition: c})
		}
	}
	slices.SortFunc(out, func(a, b PolicyCondition) int {
		if a.Policy != b.Policy {
			return strings.Compare(a.Policy, b.Policy)
		}
		return strings.Compare(a.Condition.Metric, b.Condition.Metric)
	})
	return slices.CompactFunc(out, func(a, b PolicyCondition) bool { return a == b })
}

// PolicyCondition is a condition together with the policy that requires it.
type PolicyCondition struct {
	Policy    string // policy id, for the trail back to the intent
	Name      string // the policy's human name, for the console
	Condition Condition
}

// Governor records that a key at some scope is contributed by policies rather
// than being a free local choice.
type Governor struct {
	// Policies are the ids contributing this key, sorted. More than one is
	// normal: a general policy and a stricter one can both carry it.
	Policies []string
	// Names are the same policies' human names, index-aligned with Policies.
	Names []string
	// Enforced is true when at least one of them locks the key. That is the
	// difference that matters to whoever is looking at the editor: a
	// contributed key can still be overridden here, a locked one cannot, and
	// an editor that offers the same affordance for both is lying about one.
	Enforced bool
}

// Governors returns, for a scope ref ("org", "group:<g>" or "device:<tag>"),
// the settings that policies applying AT OR ABOVE it contribute.
//
// ADR 0017 asks that a setting under governance not present itself as a free
// local choice. Answering that needs a scope-level view: Resolve answers for
// one device, and the settings editor is opened on groups and on the
// organisation far more often than on a single machine.
//
// At or above, not below: a policy assigned to a child group does govern some
// of this scope's devices, but it cannot alter what this scope's own value
// means, and flagging it here would call a key governed on a page where the
// governance does not apply.
func (f *Fleet) Governors(scope string) map[string]Governor {
	applies := f.scopeAppliesTo(scope)
	out := map[string]Governor{}
	for _, a := range f.Assignments {
		p, ok := f.Policies[a.Policy]
		if !ok || !applies(a.Target) {
			continue
		}
		locked := enforcedSet(p.Enforced)
		for key := range p.Settings {
			g := out[key]
			if !slices.Contains(g.Policies, a.Policy) {
				g.Policies = append(g.Policies, a.Policy)
				g.Names = append(g.Names, p.Name)
			}
			g.Enforced = g.Enforced || locked[key]
			out[key] = g
		}
	}
	for key, g := range out {
		// Sort the pair together so a name never drifts onto another id.
		idx := make([]int, len(g.Policies))
		for i := range idx {
			idx[i] = i
		}
		slices.SortFunc(idx, func(x, y int) int { return strings.Compare(g.Policies[x], g.Policies[y]) })
		ids, names := make([]string, len(idx)), make([]string, len(idx))
		for i, j := range idx {
			ids[i], names[i] = g.Policies[j], g.Names[j]
		}
		g.Policies, g.Names = ids, names
		out[key] = g
	}
	return out
}

// scopeAppliesTo builds the predicate "does an assignment target reach this
// scope". A device answers through its own chain, which already handles group
// ancestry and membership; the coarser scopes answer structurally.
func (f *Fleet) scopeAppliesTo(scope string) func(target string) bool {
	if tag, ok := strings.CutPrefix(scope, "device:"); ok {
		pos := f.scopePositions(tag)
		return func(target string) bool {
			_, applies := assignmentPosition(pos, target, tag)
			return applies
		}
	}
	if g, ok := strings.CutPrefix(scope, "group:"); ok {
		reach := map[string]bool{"org": true}
		for _, anc := range f.GroupAncestry(g) { // root -> specific, includes g
			reach["group:"+anc] = true
		}
		return func(target string) bool { return reach[target] }
	}
	return func(target string) bool { return target == "org" } // org sees only org
}
