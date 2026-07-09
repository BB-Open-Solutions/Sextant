package fleet

import (
	"fmt"
	"strings"
)

// Filter attribute names and operators. The vocabulary is deliberately
// closed: filters select devices by known attributes, they never evaluate
// user-supplied code.
const (
	AttrTag          = "tag"
	AttrClass        = "class"
	AttrHardware     = "hardware"
	AttrAssignedUser = "assignedUser"
	AttrGroup        = "group" // membership, including group ancestry
	labelPrefix      = "label:"

	OpEq     = "eq"
	OpNe     = "ne"
	OpPrefix = "prefix"
	OpIn     = "in"

	MatchAll = "all"
	MatchAny = "any"
)

// ValidateFilter rejects malformed filters at write time so resolution never
// sees an invalid rule.
func ValidateFilter(fl Filter) error {
	switch fl.Match {
	case "", MatchAll, MatchAny:
	default:
		return fmt.Errorf("filter match %q: must be all or any", fl.Match)
	}
	if len(fl.Rules) == 0 {
		return fmt.Errorf("filter has no rules")
	}
	for i, r := range fl.Rules {
		if !validAttr(r.Attr) {
			return fmt.Errorf("rule %d: unknown attribute %q", i, r.Attr)
		}
		switch r.Op {
		case OpEq, OpNe, OpPrefix:
			if r.Value == "" {
				return fmt.Errorf("rule %d: op %s needs a value", i, r.Op)
			}
		case OpIn:
			if len(r.Values) == 0 {
				return fmt.Errorf("rule %d: op in needs values", i)
			}
		default:
			return fmt.Errorf("rule %d: unknown op %q", i, r.Op)
		}
	}
	return nil
}

func validAttr(a string) bool {
	switch a {
	case AttrTag, AttrClass, AttrHardware, AttrAssignedUser, AttrGroup:
		return true
	}
	return strings.HasPrefix(a, labelPrefix) && len(a) > len(labelPrefix)
}

// matchesFilter reports whether the device (identified by tag) satisfies the
// filter. Unknown attributes or ops never match: a malformed rule fails
// closed rather than silently applying policy too broadly.
func (f *Fleet) matchesFilter(fl Filter, tag string) bool {
	match := fl.Match
	if match == "" {
		match = MatchAll
	}
	if len(fl.Rules) == 0 {
		return false // an empty filter selects nothing; "no filter" is Assignment.Filter == ""
	}
	for _, r := range fl.Rules {
		ok := f.matchesRule(r, tag)
		if match == MatchAll && !ok {
			return false
		}
		if match == MatchAny && ok {
			return true
		}
	}
	return match == MatchAll
}

func (f *Fleet) matchesRule(r FilterRule, tag string) bool {
	d, exists := f.Devices[tag]
	if !exists {
		return false
	}

	// Group membership tests the expanded ancestry: a filter on a parent
	// group matches devices in its subgroups too.
	if r.Attr == AttrGroup {
		in := map[string]bool{}
		for _, g := range d.Groups {
			for _, anc := range f.GroupAncestry(g) {
				in[anc] = true
			}
		}
		switch r.Op {
		case OpEq:
			return in[r.Value]
		case OpNe:
			return !in[r.Value]
		case OpPrefix:
			for g := range in {
				if strings.HasPrefix(g, r.Value) {
					return true
				}
			}
			return false
		case OpIn:
			for _, v := range r.Values {
				if in[v] {
					return true
				}
			}
			return false
		}
		return false
	}

	var got string
	switch {
	case r.Attr == AttrTag:
		got = tag
	case r.Attr == AttrClass:
		got = d.Class
	case r.Attr == AttrHardware:
		got = d.Hardware
	case r.Attr == AttrAssignedUser:
		got = d.AssignedUser
	case strings.HasPrefix(r.Attr, labelPrefix):
		got = d.Labels[strings.TrimPrefix(r.Attr, labelPrefix)]
	default:
		return false
	}

	switch r.Op {
	case OpEq:
		return got == r.Value
	case OpNe:
		return got != r.Value
	case OpPrefix:
		return got != "" && strings.HasPrefix(got, r.Value)
	case OpIn:
		for _, v := range r.Values {
			if got == v {
				return true
			}
		}
	}
	return false
}
