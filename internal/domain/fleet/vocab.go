package fleet

import "slices"

// vocab.go: the controlled device-class vocabulary. One source of truth for
// both the console dropdowns (so an operator picks, never free-types) and the
// ValidClass guardrail that the mutations enforce - the two cannot drift.

// Classes is the canonical set of device classes. Order is display order.
var Classes = []string{"laptop", "desktop", "server", "station"}

// ValidClass reports whether s is a known device class. The empty string is
// deliberately invalid: a class must be an explicit, recognised choice, not an
// accidental blank.
func ValidClass(s string) bool {
	return slices.Contains(Classes, s)
}
