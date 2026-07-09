package fleet

import (
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

// ValidateFlatpak reports whether n is a safe flathub app id.
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
