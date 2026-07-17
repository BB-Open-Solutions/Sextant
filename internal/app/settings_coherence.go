package app

import (
	"fmt"
)

// settings_coherence.go: refuses setting combinations that are INVALID on a
// device, as opposed to merely inert (product-stability principle, Bram 18
// jul: customers must not be able to break a device; the console blocks known
// invalid combinations before the gate has to). Inert-but-harmless
// combinations (an option whose enable is off) are surfaced in the editor,
// never blocked - staging a value before flipping its enable is legitimate.

// exclusivePairs lists settings that must never both be true for one device.
// Today: the two desktop stacks (one display manager wins, the other breaks).
// The structural fix is one enum option in the core; until that lands this
// guard keeps the pair impossible to save.
var exclusivePairs = [][2]string{
	{"desktop.gnome.enable", "desktop.plasma.enable"},
}

// GuardExclusiveSettings rejects a batch of changes at scope when the result
// would turn both halves of an exclusive pair on for any affected device.
// Effective values are approximated as "the batch's new value at this scope,
// otherwise the currently resolved value" - a more specific scope overriding
// the pair away is rare, and a false block only asks the operator to be
// explicit; the nix gate remains the final authority.
func GuardExclusiveSettings(cfg *ConfigService, scope string, changes []SettingChange) error {
	if cfg == nil {
		return nil
	}
	changed := map[string]string{}
	for _, c := range changes {
		if !c.Clear {
			changed[c.Key] = c.RawValue
		}
	}
	touches := false
	for _, pair := range exclusivePairs {
		if _, a := changed[pair[0]]; a {
			touches = true
		}
		if _, b := changed[pair[1]]; b {
			touches = true
		}
	}
	if !touches {
		return nil
	}

	f := cfg.Fleet()
	effective := func(tag, key string) bool {
		if raw, ok := changed[key]; ok {
			return raw == "true"
		}
		v, _ := f.Resolve(tag)[key].Value.(bool)
		return v
	}
	for _, tag := range AffectedHosts(f, scope) {
		for _, pair := range exclusivePairs {
			if effective(tag, pair[0]) && effective(tag, pair[1]) {
				return fmt.Errorf("invalid combination: %s and %s cannot both be enabled (device %s would get two desktop stacks). Enable one and disable the other in the same save",
					pair[0], pair[1], tag)
			}
		}
	}
	return nil
}
