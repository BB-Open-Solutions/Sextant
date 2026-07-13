package fleet

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// apps.go: packages, flatpaks and overlays are additive across the scope
// chain (org + group ancestry + device, unioned), unlike settings. Ported
// from the proven PoC implementation.

// The injection firewall for app data: data may only NAME a package, flatpak
// or overlay (a lookup), never inject nix. Anything with spaces, quotes,
// interpolation, slashes or dot-dot is rejected.
var (
	pkgNameRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._-]*$`)
	overlayName = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)
)

// ValidatePackage reports whether n is a safe nixpkgs attribute name
// (dotted paths like python3Packages.requests allowed).
func ValidatePackage(n string) bool { return pkgNameRe.MatchString(n) && !strings.Contains(n, "..") }

// ValidateFlatpak reports whether n is a safe name: it applies the same
// injection firewall as ValidatePackage (pkgNameRe, no ".."). It does not
// verify the reverse-DNS flathub id shape (e.g. org.mozilla.firefox).
func ValidateFlatpak(n string) bool { return pkgNameRe.MatchString(n) && !strings.Contains(n, "..") }

// ValidateOverlay reports whether n is a safe overlay name.
func ValidateOverlay(n string) bool { return overlayName.MatchString(n) }

// dedup trims, deduplicates and sorts.
func dedup(in []string) []string {
	seen := map[string]bool{}
	out := in[:0:0]
	for _, s := range in {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// AppKind names one of the three additive app lists.
type AppKind string

const (
	AppPackages AppKind = "packages"
	AppFlatpaks AppKind = "flatpaks"
	AppOverlays AppKind = "overlays"
)

// validateApp checks one name against its kind's injection firewall.
func validateApp(kind AppKind, name string) error {
	ok := false
	switch kind {
	case AppPackages:
		ok = ValidatePackage(name)
	case AppFlatpaks:
		ok = ValidateFlatpak(name)
	case AppOverlays:
		ok = ValidateOverlay(name)
	default:
		return fmt.Errorf("unknown app kind %q (packages|flatpaks|overlays)", kind)
	}
	if !ok {
		return fmt.Errorf("invalid %s name %q", kind, name)
	}
	return nil
}

// SetScopeApps replaces one app list at a scope. Every name passes the
// injection firewall; the list is deduplicated and sorted, so writes are
// deterministic and diffs stay readable.
func SetScopeApps(ref string, kind AppKind, names []string) Mutation {
	return func(f *Fleet) error {
		clean := dedup(names)
		for _, n := range clean {
			if err := validateApp(kind, n); err != nil {
				return err
			}
		}
		return f.withApps(ref, func(pkgs, flat, ov *[]string) {
			switch kind {
			case AppPackages:
				*pkgs = clean
			case AppFlatpaks:
				*flat = clean
			case AppOverlays:
				*ov = clean
			}
		})
	}
}

// withApps edits a scope's app lists in place, mirroring withScope.
func (f *Fleet) withApps(ref string, edit func(pkgs, flat, ov *[]string)) error {
	switch {
	case ref == "org":
		if f.Org == nil {
			f.Org = &Scope{}
		}
		edit(&f.Org.Packages, &f.Org.Flatpaks, &f.Org.Overlays)
		return nil
	case strings.HasPrefix(ref, "group:"):
		name := strings.TrimPrefix(ref, "group:")
		g, ok := f.Groups[name]
		if !ok {
			return fmt.Errorf("unknown group %q", name)
		}
		edit(&g.Packages, &g.Flatpaks, &g.Overlays)
		f.Groups[name] = g
		return nil
	case strings.HasPrefix(ref, "device:"):
		tag := strings.TrimPrefix(ref, "device:")
		d, ok := f.Devices[tag]
		if !ok {
			return fmt.Errorf("unknown device %q", tag)
		}
		edit(&d.Packages, &d.Flatpaks, &d.Overlays)
		f.Devices[tag] = d
		return nil
	}
	return fmt.Errorf("bad scope %q (want org|group:<name>|device:<tag>)", ref)
}

func (f *Fleet) appLists(kind, name string) (pkgs, flat, ov []string) {
	switch kind {
	case "org":
		if f.Org != nil {
			return f.Org.Packages, f.Org.Flatpaks, f.Org.Overlays
		}
	case "group":
		g := f.Groups[name]
		return g.Packages, g.Flatpaks, g.Overlays
	case "device":
		d := f.Devices[name]
		return d.Packages, d.Flatpaks, d.Overlays
	}
	return nil, nil, nil
}

// ResolveApps unions the apps a device gets from org, its group ancestry and
// itself (additive; deduplicated and sorted). The nix generator turns these
// into environment.systemPackages, flatpak installs and nixpkgs.overlays.
func (f *Fleet) ResolveApps(tag string) (packages, flatpaks, overlays []string) {
	d := f.Devices[tag]
	add := func(p, fl, o []string) {
		packages = append(packages, p...)
		flatpaks = append(flatpaks, fl...)
		overlays = append(overlays, o...)
	}
	add(f.appLists("org", ""))
	for _, g := range d.Groups {
		for _, anc := range f.GroupAncestry(g) {
			add(f.appLists("group", anc))
		}
	}
	add(f.appLists("device", tag))
	return dedup(packages), dedup(flatpaks), dedup(overlays)
}
