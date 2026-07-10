package fleet

import (
	"fmt"
	"slices"
)

// device.go: enrollment mutations. Adding a device is the front door of the
// fleet: the tag becomes a nix host attribute and a git path, so it must be
// a safe slug; the groups must exist; the gate then proves the generator
// accepts the new host before anything is committed.

// AddDevice enrolls a new device. Hardware names the overlay's hardware
// profile and is required; unknown groups are rejected.
func AddDevice(tag string, d Device) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(tag) {
			return fmt.Errorf("invalid device tag %q (lowercase slug required)", tag)
		}
		if _, exists := f.Devices[tag]; exists {
			return fmt.Errorf("device %q already exists", tag)
		}
		if d.Hardware == "" {
			return fmt.Errorf("device needs a hardware profile")
		}
		for _, g := range d.Groups {
			if _, ok := f.Groups[g]; !ok {
				return fmt.Errorf("unknown group %q", g)
			}
		}
		if f.Devices == nil {
			f.Devices = map[string]Device{}
		}
		f.Devices[tag] = d
		return nil
	}
}

// RemoveDevice unenrolls a device and drops its policy assignments.
func RemoveDevice(tag string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Devices[tag]; !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		delete(f.Devices, tag)
		f.Assignments = slices.DeleteFunc(f.Assignments, func(a Assignment) bool {
			return a.Target == "device:"+tag
		})
		return nil
	}
}

// DevicePatch updates a device in place; nil fields stay untouched. Group
// membership, assignment and hardware changes all pass the gate before
// anything commits, like every mutation.
type DevicePatch struct {
	Hardware     *string
	Class        *string
	AssignedUser *string
	Groups       *[]string
	Labels       *map[string]string
	ITAM         *ITAM
}

// UpdateDevice applies a patch to an enrolled device.
func UpdateDevice(tag string, p DevicePatch) Mutation {
	return func(f *Fleet) error {
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		if p.Hardware != nil {
			if *p.Hardware == "" {
				return fmt.Errorf("device needs a hardware profile")
			}
			d.Hardware = *p.Hardware
		}
		if p.Class != nil {
			d.Class = *p.Class
		}
		if p.AssignedUser != nil {
			d.AssignedUser = *p.AssignedUser
		}
		if p.Groups != nil {
			for _, g := range *p.Groups {
				if _, ok := f.Groups[g]; !ok {
					return fmt.Errorf("unknown group %q", g)
				}
			}
			d.Groups = *p.Groups
		}
		if p.Labels != nil {
			d.Labels = *p.Labels
		}
		if p.ITAM != nil {
			d.ITAM = *p.ITAM
		}
		f.Devices[tag] = d
		return nil
	}
}

// RetireDevice parks a device: the record and its audit trail stay, image
// builds, check-ins and rollout counting stop. The caller must also revoke
// the device credential (app layer owns that store).
func RetireDevice(tag string) Mutation {
	return setDeviceState(tag, DeviceRetired, DeviceActive)
}

// ReactivateDevice returns a retired device to service.
func ReactivateDevice(tag string) Mutation {
	return setDeviceState(tag, DeviceActive, DeviceRetired)
}

func setDeviceState(tag, to, from string) Mutation {
	return func(f *Fleet) error {
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		if d.State != from {
			return fmt.Errorf("device %q is %s, not %s", tag, stateName(d.State), stateName(from))
		}
		d.State = to
		f.Devices[tag] = d
		return nil
	}
}

func stateName(s string) string {
	if s == DeviceActive {
		return "active"
	}
	return s
}

// AddGroup creates a group; parent must exist when named.
func AddGroup(name string, g Group) Mutation {
	return func(f *Fleet) error {
		if !ValidateSlug(name) {
			return fmt.Errorf("invalid group name %q (lowercase slug required)", name)
		}
		if _, exists := f.Groups[name]; exists {
			return fmt.Errorf("group %q already exists", name)
		}
		if g.Parent != "" {
			if _, ok := f.Groups[g.Parent]; !ok {
				return fmt.Errorf("unknown parent group %q", g.Parent)
			}
		}
		if f.Groups == nil {
			f.Groups = map[string]Group{}
		}
		f.Groups[name] = g
		return nil
	}
}

// UpdateGroup changes a group's parent and/or IdP mapping. Re-parenting
// reuses SetGroupParent's cycle guard.
func UpdateGroup(name string, parent, idpGroup *string) Mutation {
	return func(f *Fleet) error {
		g, ok := f.Groups[name]
		if !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		if parent != nil {
			if err := SetGroupParent(name, *parent)(f); err != nil {
				return err
			}
			g = f.Groups[name]
		}
		if idpGroup != nil {
			g.IdpGroup = *idpGroup
			f.Groups[name] = g
		}
		return nil
	}
}

// RemoveGroup deletes an empty leaf group. Anything still referencing it -
// child groups, device memberships, assignments, access bindings, rollout
// rings - blocks the removal by name, so nothing dangles.
func RemoveGroup(name string) Mutation {
	return func(f *Fleet) error {
		if _, ok := f.Groups[name]; !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		if kids := f.GroupChildren(name); len(kids) > 0 {
			return fmt.Errorf("group %q still has subgroups %v", name, kids)
		}
		if devs := f.GroupDevices(name); len(devs) > 0 {
			return fmt.Errorf("group %q still has devices %v", name, devs)
		}
		ref := "group:" + name
		for _, a := range f.Assignments {
			if a.Target == ref {
				return fmt.Errorf("group %q still targeted by assignment of policy %q", name, a.Policy)
			}
		}
		for _, b := range f.Access {
			if b.Scope == ref {
				return fmt.Errorf("group %q still carries an access binding for %q", name, b.Group)
			}
		}
		if f.Rollout != nil {
			for _, ring := range f.Rollout.Rings {
				if ring.Group == name {
					return fmt.Errorf("group %q is still a rollout ring", name)
				}
			}
		}
		delete(f.Groups, name)
		return nil
	}
}
