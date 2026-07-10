package fleet

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

// mutate.go holds the pure mutations. Each returns a function applied to a
// *Fleet by the write transaction (mutate -> gate -> commit); a returned
// error aborts the transaction before anything reaches disk or git.

// slugRe guards every user-supplied identifier that ends up in file paths,
// git refs or nix attribute names.
var slugRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidateSlug reports whether s is a safe identifier (group names, device
// tags, policy and filter ids).
func ValidateSlug(s string) bool { return slugRe.MatchString(s) }

// Mutation transforms the fleet document inside a write transaction.
type Mutation func(*Fleet) error

// withScope runs edit against a scope ref's settings and enforced list and
// writes the result back into the document.
func (f *Fleet) withScope(ref string, edit func(settings *map[string]any, enforced *[]string) error) error {
	switch {
	case ref == "org":
		if f.Org == nil {
			f.Org = &Scope{}
		}
		return edit(&f.Org.Settings, &f.Org.Enforced)
	case strings.HasPrefix(ref, "group:"):
		name := strings.TrimPrefix(ref, "group:")
		g, ok := f.Groups[name]
		if !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		if err := edit(&g.Settings, &g.Enforced); err != nil {
			return err
		}
		f.Groups[name] = g
		return nil
	case strings.HasPrefix(ref, "device:"):
		tag := strings.TrimPrefix(ref, "device:")
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		if err := edit(&d.Settings, &d.Enforced); err != nil {
			return err
		}
		f.Devices[tag] = d
		return nil
	}
	return fmt.Errorf("bad scope %q (want org|group:<name>|device:<tag>)", ref)
}

// ScopeSettings reads a scope's own inline settings and enforced list (the
// values set at exactly this scope, not the resolved chain). Read-only
// counterpart of withScope. Returns copies: fleet snapshots are shared and
// immutable, and this API must not hand out a mutable window into one.
func (f *Fleet) ScopeSettings(ref string) (map[string]any, []string, error) {
	copied := func(settings map[string]any, enforced []string) (map[string]any, []string, error) {
		return maps.Clone(settings), slices.Clone(enforced), nil
	}
	switch {
	case ref == "org":
		if f.Org == nil {
			return nil, nil, nil
		}
		return copied(f.Org.Settings, f.Org.Enforced)
	case strings.HasPrefix(ref, "group:"):
		g, ok := f.Groups[strings.TrimPrefix(ref, "group:")]
		if !ok {
			return nil, nil, fmt.Errorf("unknown group %q", strings.TrimPrefix(ref, "group:"))
		}
		return copied(g.Settings, g.Enforced)
	case strings.HasPrefix(ref, "device:"):
		d, ok := f.Devices[strings.TrimPrefix(ref, "device:")]
		if !ok {
			return nil, nil, fmt.Errorf("unknown device %q", strings.TrimPrefix(ref, "device:"))
		}
		return copied(d.Settings, d.Enforced)
	}
	return nil, nil, fmt.Errorf("bad scope %q (want org|group:<name>|device:<tag>)", ref)
}

// SetScopeSetting sets one setting key at a scope.
func SetScopeSetting(ref, key string, value any) Mutation {
	return func(f *Fleet) error {
		return f.withScope(ref, func(settings *map[string]any, _ *[]string) error {
			if *settings == nil {
				*settings = map[string]any{}
			}
			(*settings)[key] = value
			return nil
		})
	}
}

// ClearScopeSetting removes a setting (and its enforce mark) from a scope,
// reverting to whatever the chain inherits.
func ClearScopeSetting(ref, key string) Mutation {
	return func(f *Fleet) error {
		return f.withScope(ref, func(settings *map[string]any, enforced *[]string) error {
			delete(*settings, key)
			*enforced = slices.DeleteFunc(*enforced, func(k string) bool { return k == key })
			return nil
		})
	}
}

// SetScopeEnforce marks or unmarks a scope's setting as enforced. Enforcing
// a key that has no value at that scope is rejected: a lock must lock a value.
func SetScopeEnforce(ref, key string, on bool) Mutation {
	return func(f *Fleet) error {
		return f.withScope(ref, func(settings *map[string]any, enforced *[]string) error {
			if on {
				if _, has := (*settings)[key]; !has {
					return fmt.Errorf("cannot enforce %q at %s: no value set at that scope", key, ref)
				}
				if !slices.Contains(*enforced, key) {
					*enforced = append(*enforced, key)
				}
				return nil
			}
			*enforced = slices.DeleteFunc(*enforced, func(k string) bool { return k == key })
			return nil
		})
	}
}

// SetRolloutPlan replaces the staged-rollout plan. Every ring must name an
// existing group exactly once with sane soak and health bounds; nil clears
// the plan entirely.
func SetRolloutPlan(p *RolloutPolicy) Mutation {
	return func(f *Fleet) error {
		if p == nil {
			f.Rollout = nil
			return nil
		}
		if len(p.Rings) == 0 {
			return fmt.Errorf("rollout plan needs at least one ring (or clear the plan)")
		}
		seen := map[string]bool{}
		for i, ring := range p.Rings {
			if _, ok := f.Groups[ring.Group]; !ok {
				return fmt.Errorf("ring %d: unknown group %q", i, ring.Group)
			}
			if seen[ring.Group] {
				return fmt.Errorf("ring %d: group %q appears twice", i, ring.Group)
			}
			seen[ring.Group] = true
			if ring.SoakMinutes < 0 {
				return fmt.Errorf("ring %d: negative soak", i)
			}
			if ring.MinHealthyPercent < 0 || ring.MinHealthyPercent > 100 {
				return fmt.Errorf("ring %d: minHealthyPercent must be 0-100", i)
			}
		}
		f.Rollout = p
		return nil
	}
}

// SetAssurance replaces the audit-control configuration.
func SetAssurance(a Assurance) Mutation {
	return func(f *Fleet) error {
		f.Assurance = &a
		return nil
	}
}

// SetGroupParent re-parents a group; a link that would form a cycle is
// refused. Empty parent detaches the group to root.
func SetGroupParent(group, parent string) Mutation {
	return func(f *Fleet) error {
		g, ok := f.Groups[group]
		if !ok {
			return fmt.Errorf("unknown group %q", group)
		}
		if parent != "" {
			if _, ok := f.Groups[parent]; !ok {
				return fmt.Errorf("unknown parent group %q", parent)
			}
			// Walking up from the new parent must never reach the group itself.
			seen := map[string]bool{}
			for cur := parent; cur != ""; {
				if cur == group {
					return fmt.Errorf("parent %q would create a cycle through %q", parent, group)
				}
				if seen[cur] {
					break
				}
				seen[cur] = true
				cur = f.Groups[cur].Parent
			}
		}
		g.Parent = parent
		f.Groups[group] = g
		return nil
	}
}

// SetGroupPin pins a group (a rollout ring) to a target revision; an empty
// target unpins (the group follows HEAD). The pin is config-as-data: it
// rides the gated write transaction and lands as an audited commit.
func SetGroupPin(group, target string) Mutation {
	return func(f *Fleet) error {
		g, ok := f.Groups[group]
		if !ok {
			return fmt.Errorf("unknown group %q", group)
		}
		g.Pin = target
		f.Groups[group] = g
		return nil
	}
}

// SetAcceptance documents a risk acceptance (comply-or-explain) at a scope.
// An empty justification is rejected: the explanation is the point.
func SetAcceptance(ref, key, reason string) Mutation {
	return func(f *Fleet) error {
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return fmt.Errorf("acceptance for %q needs a documented justification", key)
		}
		return f.withAccepted(ref, func(acc *map[string]string) {
			if *acc == nil {
				*acc = map[string]string{}
			}
			(*acc)[key] = reason
		})
	}
}

// ClearAcceptance removes a risk acceptance at a scope.
func ClearAcceptance(ref, key string) Mutation {
	return func(f *Fleet) error {
		return f.withAccepted(ref, func(acc *map[string]string) {
			delete(*acc, key)
		})
	}
}

func (f *Fleet) withAccepted(ref string, edit func(*map[string]string)) error {
	switch {
	case ref == "org":
		if f.Org == nil {
			f.Org = &Scope{}
		}
		edit(&f.Org.Accepted)
		return nil
	case strings.HasPrefix(ref, "group:"):
		name := strings.TrimPrefix(ref, "group:")
		g, ok := f.Groups[name]
		if !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		edit(&g.Accepted)
		f.Groups[name] = g
		return nil
	case strings.HasPrefix(ref, "device:"):
		tag := strings.TrimPrefix(ref, "device:")
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		edit(&d.Accepted)
		f.Devices[tag] = d
		return nil
	}
	return fmt.Errorf("bad scope %q", ref)
}
