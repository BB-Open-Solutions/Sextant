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
		if len(p.Settings) == 0 {
			return fmt.Errorf("policy %q has no settings", id)
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
