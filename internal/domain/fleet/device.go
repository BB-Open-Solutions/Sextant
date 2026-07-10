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
