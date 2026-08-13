package fleet

import (
	"fmt"
	"sort"
)

// conflicts.go: two policies that set the same key at the same scope.
//
// ADR 0026 removed the priority number that used to settle this. What settles
// it now is declaration order, which is deterministic and written down - and
// which nobody should have to reason about, because two policies fighting
// over one key at one scope is a modelling mistake, not a preference. The
// answer is to make one of them more specific, or to take the key out of one.
//
// So the console reports the collision instead of letting the tie-break hide
// it. This is a property of the fleet DOCUMENT, not of a device: it is found
// by reading assignments, and it does not need a device to exist yet.

// equalValue compares two setting values the way the document means them.
// Settings arrive from JSON, so a value is a string, a bool, a number or a
// list; formatting both and comparing the text is exact for those and cannot
// panic on an unhashable type, which reflect.DeepEqual on interfaces can be
// surprising about.
func equalValue(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) }

// PolicyConflict is one key claimed by two policies at one target.
type PolicyConflict struct {
	// Key is the setting they disagree about.
	Key string
	// Target is the scope where they meet ("org", "group:x", "device:y").
	Target string
	// Winner takes effect (first declared); Loser does not.
	Winner, Loser string
	// Filters names the filters on the two assignments, when they carry any.
	// A filter can make the collision theoretical - two disjoint filters never
	// meet on one device - so the reader is told rather than reassured.
	Filters []string
}

// PolicyConflicts lists every key two assignments claim at the same target
// with different values, in a stable order.
//
// Same VALUE is not a conflict: two policies agreeing that secureboot is on
// is redundancy, and reporting it would train people to ignore the list.
func (f Fleet) PolicyConflicts() []PolicyConflict {
	var out []PolicyConflict
	for i := range f.Assignments {
		a := f.Assignments[i]
		pa, ok := f.Policies[a.Policy]
		if !ok {
			continue // dangling assignment; rejected at write time
		}
		for j := i + 1; j < len(f.Assignments); j++ {
			b := f.Assignments[j]
			if b.Target != a.Target {
				continue // different scopes: specificity decides, not order
			}
			pb, ok := f.Policies[b.Policy]
			if !ok || a.Policy == b.Policy {
				continue
			}
			for k, va := range pa.Settings {
				vb, has := pb.Settings[k]
				if !has || equalValue(va, vb) {
					continue
				}
				c := PolicyConflict{Key: k, Target: a.Target, Winner: a.Policy, Loser: b.Policy}
				for _, fl := range []string{a.Filter, b.Filter} {
					if fl != "" {
						c.Filters = append(c.Filters, fl)
					}
				}
				out = append(out, c)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Target != out[j].Target {
			return out[i].Target < out[j].Target
		}
		if out[i].Key != out[j].Key {
			return out[i].Key < out[j].Key
		}
		return out[i].Loser < out[j].Loser
	})
	return out
}

// InertPriorities names the assignments still carrying a priority that no
// longer does anything (ADR 0026). An operator who set one deserves to be
// told it is inert, rather than finding out when a device takes the other
// value.
func (f Fleet) InertPriorities() []Assignment {
	var out []Assignment
	for _, a := range f.Assignments {
		if a.Priority != 0 {
			out = append(out, a)
		}
	}
	return out
}
