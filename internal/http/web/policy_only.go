package web

// policy_only.go: the controls that may be set through a POLICY and nowhere
// else (ADR 0017).
//
// The general settings editor is the right home for an ordinary operational
// choice - a keyboard layout, a time zone. It is the wrong home for a control
// whose whole point is that somebody can be shown, later, why it is the way it
// is. Those need a name, a reason and a lock, and that is what a policy is.
//
// The list is short and each entry has to earn its place, because every key
// here is one an operator can no longer change from the obvious page. The test
// is not "is this important" - most settings are important. It is: would a
// quiet local override of this be a FINDING? If yes, it belongs to governance.

import "strings"

// policyOnlyKeys are settings the general editor refuses to offer.
//
// Grouped by prefix rather than listed key by key, because the members of a
// group are one decision. USB device control is the clearest case: enabling it
// without the right allowlist is exactly how a machine locks its own user out,
// which is why the enable carries riskClass high. Putting the switch on one
// page and the allowlist on another would make that dangerous combination
// EASIER to create by accident - somebody flips it where the list is not in
// view. They move together or not at all.
var policyOnlyKeys = []string{
	// USB device control: the switch and its allowlist, together.
	"usbDevices.",
	// Posture. Secure Boot and disk encryption are decided when a device is
	// imaged and are the first things an auditor asks about; a device quietly
	// exempted from either is precisely the finding a policy exists to prevent.
	"secureboot.",
	"diskEncryption",
}

// isPolicyOnly reports whether a catalog key may only be set through a policy.
func isPolicyOnly(key string) bool {
	for _, p := range policyOnlyKeys {
		if key == p || strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}
