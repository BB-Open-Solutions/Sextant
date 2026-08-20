package fleet

import (
	"fmt"
	"maps"
	"slices"
	"strings"
)

// hardware_config.go configures one hardware profile: settings that follow the
// machine rather than the organisation.
//
// It adds NO resolution rule. A Lenovo needing a fingerprint driver that a Dell
// in the same group must not get is a policy with a hardware filter, which the
// resolver has always supported (ADR 0027). What was missing is the assembly:
// a filter, a policy and an assignment, three hand edits in the right order, so
// nobody did it. This is those three as one mutation.

// HardwarePolicyID and HardwareFilterID name what configuring a model creates.
// Derived and not free-form on purpose: the pair has to be findable again to
// be refreshed, and an operator should not have to remember what they called
// it last time.
func HardwarePolicyID(profile string) string { return "hw-" + profile }

// HardwareFilterID names the filter that selects exactly one model.
func HardwareFilterID(profile string) string { return "hardware-" + profile }

// ConfigureHardware writes settings for every device carrying one hardware
// profile: a filter that selects exactly that model, a policy holding the
// settings, and an assignment binding them at target ("org" or "group:<name>").
//
// Idempotent. Called again it refreshes the settings and leaves the rest, so
// editing a model's configuration is the same operation as creating it.
//
// Empty settings REMOVE the configuration rather than writing a policy that
// says nothing: a model an operator emptied out should stop reaching devices,
// not linger as an assignment with nothing in it.
func ConfigureHardware(profile, target string, settings map[string]any, enforced []string) Mutation {
	return func(f *Fleet) error {
		profile = strings.TrimSpace(profile)
		if profile == "" {
			return fmt.Errorf("configuring hardware needs a profile name")
		}
		polID, filterID := HardwarePolicyID(profile), HardwareFilterID(profile)
		if !ValidateSlug(polID) || !ValidateSlug(filterID) {
			// The name comes from the imaging catalog or from what devices
			// already carry, so this is reachable with a hand-edited overlay.
			// Say which name, because "invalid slug" about a derived id is a
			// riddle.
			return fmt.Errorf("hardware profile %q cannot be configured: its name is not a lowercase slug", profile)
		}
		if err := validHardwareTarget(f, target); err != nil {
			return err
		}

		if len(settings) == 0 {
			return unconfigureHardware(f, polID, filterID)
		}

		// Reusing a same-named filter is only safe while it still means what
		// the name says: exactly this one model. Anything else would silently
		// point the model's settings at a different set of devices. Same guard
		// as ApplyProfile, and for the same reason.
		if ex, ok := f.Filters[filterID]; ok {
			if !selectsExactly(ex, AttrHardware, profile) {
				return fmt.Errorf("filter %q exists but does not select hardware %q; rename or remove it first", filterID, profile)
			}
		} else if err := PutFilter(filterID, Filter{
			Rules: []FilterRule{{Attr: AttrHardware, Op: OpEq, Value: profile}},
		})(f); err != nil {
			return err
		}

		// A policy that came from an overlay profile is not ours to overwrite:
		// re-applying that profile would fight this write every time.
		pol := Policy{Name: profile, Settings: maps.Clone(settings), Enforced: slices.Clone(enforced)}
		if prev, ok := f.Policies[polID]; ok {
			if prev.Profile != "" {
				return fmt.Errorf("policy %q came from overlay profile %q; configure the model under a different name", polID, prev.Profile)
			}
			if prev.Name != "" {
				pol.Name = prev.Name
			}
			pol.Description, pol.Controls = prev.Description, prev.Controls
		}
		if err := PutPolicy(polID, pol)(f); err != nil {
			return err
		}

		// One assignment for this pair. A target change moves it rather than
		// adding a second: two assignments of one policy on one model is not a
		// wider rollout, it is a mistake that resolves to the same value twice.
		f.Assignments = slices.DeleteFunc(f.Assignments, func(a Assignment) bool {
			return a.Policy == polID && a.Filter == filterID
		})
		return Assign(Assignment{Policy: polID, Target: target, Filter: filterID})(f)
	}
}

// unconfigureHardware removes what ConfigureHardware created, and only that.
// The filter survives if anything else still uses it: it is a named thing an
// operator may have pointed another assignment at.
func unconfigureHardware(f *Fleet, polID, filterID string) error {
	f.Assignments = slices.DeleteFunc(f.Assignments, func(a Assignment) bool {
		return a.Policy == polID && a.Filter == filterID
	})
	delete(f.Policies, polID)
	stillUsed := slices.ContainsFunc(f.Assignments, func(a Assignment) bool { return a.Filter == filterID })
	if !stillUsed {
		delete(f.Filters, filterID)
	}
	return nil
}

// validHardwareTarget accepts the scopes a model's settings may be bound to.
// Not a device: settings for one device belong on that device, and binding a
// model-wide policy to a single device is a way of writing that which nobody
// reading the fleet later would recognise.
func validHardwareTarget(f *Fleet, target string) error {
	switch {
	case target == "org":
		return nil
	case strings.HasPrefix(target, "group:"):
		g := strings.TrimPrefix(target, "group:")
		if _, ok := f.Groups[g]; !ok {
			return fmt.Errorf("unknown group %q", g)
		}
		return nil
	}
	return fmt.Errorf("hardware settings bind to org or a group, not to %q", target)
}

// selectsExactly reports whether a filter is precisely "attr equals value" and
// nothing more.
func selectsExactly(fl Filter, attr, value string) bool {
	return len(fl.Rules) == 1 && fl.Rules[0].Attr == attr &&
		fl.Rules[0].Op == OpEq && fl.Rules[0].Value == value
}

// HardwareConfig reports how one profile is configured: the assignment this
// package created for it, if any.
func (f *Fleet) HardwareConfig(profile string) (Policy, Assignment, bool) {
	polID, filterID := HardwarePolicyID(profile), HardwareFilterID(profile)
	pol, ok := f.Policies[polID]
	if !ok {
		return Policy{}, Assignment{}, false
	}
	for _, a := range f.Assignments {
		if a.Policy == polID && a.Filter == filterID {
			return pol, a, true
		}
	}
	return pol, Assignment{}, false
}

// HardwareInUse counts devices per hardware profile, retired ones excluded:
// a page about models should not report a shelf.
func (f *Fleet) HardwareInUse() map[string]int {
	out := map[string]int{}
	for _, d := range f.Devices {
		if d.State == DeviceRetired || d.Hardware == "" {
			continue
		}
		out[d.Hardware]++
	}
	return out
}
