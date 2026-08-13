package fleet

import (
	"sort"
)

// resolve.go is the Go twin of the overlay's lib/resolve.nix. Both must agree
// exactly so the console shows what the nix generator builds. The precedence
// rule, unchanged from the proven PoC resolver:
//
//   - enforced: the MOST GENERAL enforcing contributor wins
//     (org beats group beats device) - governance; nix emits mkForce.
//   - default:  the MOST SPECIFIC contributor wins
//     (device beats group beats org) - flexibility; nix emits mkDefault.
//
// Policies extend the rule without changing it: assignments compile into
// contributors on the same scope chain (see chain.go). Ties at equal
// specificity break on: inline scope settings beat policy contributions,
// then higher assignment priority, then deterministic assignment order.

// Source identifies what set a resolved value: the scope ref ("org",
// "group:<name>", "device") and, when a policy delivered it, the policy id.
type Source struct {
	Scope  string `json:"scope"`
	Policy string `json:"policy,omitempty"`
}

// String renders provenance for display: "org", "group:pilot",
// "policy:baseline@org".
func (s Source) String() string {
	if s.Policy != "" {
		return "policy:" + s.Policy + "@" + s.Scope
	}
	return s.Scope
}

// Resolution is one resolved setting: the effective value, its provenance,
// and whether it is enforced (locked against more specific scopes).
type Resolution struct {
	Value    any    `json:"value"`
	Source   Source `json:"source"`
	Enforced bool   `json:"enforced"`
}

// ResolvedSetting is a Resolution keyed by its setting path, for sorted output.
type ResolvedSetting struct {
	Key string `json:"key"`
	Resolution
}

// Resolve returns the effective value and provenance of every setting that
// applies to the device, from inline scope settings and assigned policies.
func (f *Fleet) Resolve(tag string) map[string]Resolution {
	chain := f.chainFor(tag)

	keys := map[string]struct{}{}
	for _, c := range chain {
		for k := range c.settings {
			keys[k] = struct{}{}
		}
	}

	out := make(map[string]Resolution, len(keys))
	for k := range keys {
		out[k] = resolveKey(chain, k)
	}
	return out
}

// resolveKey applies the precedence rule over the compiled chain.
func resolveKey(chain []contributor, key string) Resolution {
	better := func(a, b *contributor, wantGeneral bool) bool {
		if a.specificity != b.specificity {
			if wantGeneral {
				return a.specificity < b.specificity
			}
			return a.specificity > b.specificity
		}
		if a.inline != b.inline {
			return a.inline // inline scope settings beat policy contributions
		}
		// No priority number (ADR 0026): declaration order decides, and the
		// collision itself is reported rather than settled quietly. A third
		// precedence rule for a question specificity and inline-over-policy
		// already answer is a rule somebody has to guess at.
		return a.order < b.order
	}

	// Pass 1 - enforced: most general enforcing contributor wins.
	var enf *contributor
	for i := range chain {
		c := &chain[i]
		if _, has := c.settings[key]; has && c.enforced[key] {
			if enf == nil || better(c, enf, true) {
				enf = c
			}
		}
	}
	if enf != nil {
		return Resolution{Value: enf.settings[key], Source: enf.source, Enforced: true}
	}

	// Pass 2 - default: most specific contributor wins.
	var def *contributor
	for i := range chain {
		c := &chain[i]
		if _, has := c.settings[key]; has {
			if def == nil || better(c, def, false) {
				def = c
			}
		}
	}
	if def != nil {
		return Resolution{Value: def.settings[key], Source: def.source, Enforced: false}
	}
	return Resolution{} // unreachable: key came from some contributor
}

// ResolveValues flattens Resolve to key -> effective value.
func (f *Fleet) ResolveValues(tag string) map[string]any {
	m := f.Resolve(tag)
	out := make(map[string]any, len(m))
	for k, r := range m {
		out[k] = r.Value
	}
	return out
}

// ResolveSorted returns Resolve as a key-sorted slice for stable rendering.
func (f *Fleet) ResolveSorted(tag string) []ResolvedSetting {
	m := f.Resolve(tag)
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]ResolvedSetting, 0, len(keys))
	for _, k := range keys {
		out = append(out, ResolvedSetting{Key: k, Resolution: m[k]})
	}
	return out
}
