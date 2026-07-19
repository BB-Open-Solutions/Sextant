package fleet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"
)

// profiles.go: recommended-settings profiles. A profile is a curated bundle
// of settings for one kind of device (a laptop: desktop environment,
// non-breaking hardening, NTP), authored in the overlay next to catalog.json
// - the DAWO core ships defaults, a tenant overlay may replace or extend
// them. The console instantiates a profile as a regular policy (plus a
// class filter and an org assignment), so everything a profile sets stays
// visible and overridable through the normal scope chain; a profile is a
// starting point, never a lock. The gate remains the validator: a profile
// whose settings the images reject cannot be applied.

// ProfilesFile is the profiles file's path inside the overlay repo.
const ProfilesFile = "profiles.json"

// Profile is one recommended-settings bundle.
type Profile struct {
	// Name is the slug identity; it seeds the instantiated policy's id.
	Name string `json:"name"`
	// Label is the human name the console shows ("Laptop workplace").
	Label string `json:"label,omitempty"`
	// Description explains what the profile sets up and for whom.
	Description string `json:"description,omitempty"`
	// Class narrows the profile to one device class via a class filter on
	// the instantiated assignment; empty applies fleet-wide.
	Class string `json:"class,omitempty"`
	// Settings are the recommended values, keyed like any scope's settings.
	Settings map[string]any `json:"settings"`
}

// Hash fingerprints the profile's effective content (class + settings, not
// the wording) so an instantiated policy can record which version of the
// profile it came from and the console can surface drift when the overlay's
// profile moves on. Map keys marshal sorted, so the hash is stable.
func (p Profile) Hash() string {
	b, _ := json.Marshal(struct {
		Class    string         `json:"class"`
		Settings map[string]any `json:"settings"`
	}{p.Class, p.Settings})
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:4])
}

// Provenance is the profile stamp an instantiated policy carries
// ("laptop@1a2b3c4d"): enough to name the source profile and detect drift.
func (p Profile) Provenance() string { return p.Name + "@" + p.Hash() }

// Profiles is the parsed, indexed profile set.
type Profiles struct {
	byName map[string]Profile
	order  []string // profile names, sorted, for stable rendering
}

// ParseProfiles reads profiles.json. Empty input yields an empty (never
// nil) set: an overlay without profiles is valid, the console simply offers
// none. A malformed document, a non-slug name, an empty settings map or a
// duplicate is rejected so a broken profile never renders as applyable.
func ParseProfiles(raw []byte) (*Profiles, error) {
	ps := &Profiles{byName: map[string]Profile{}}
	if len(raw) == 0 {
		return ps, nil
	}
	var list []Profile
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", ProfilesFile, err)
	}
	for _, p := range list {
		if !ValidateSlug(p.Name) {
			return nil, fmt.Errorf("%s: profile name %q: must be a lowercase slug", ProfilesFile, p.Name)
		}
		if len(p.Settings) == 0 {
			return nil, fmt.Errorf("%s: profile %q has no settings", ProfilesFile, p.Name)
		}
		if _, dup := ps.byName[p.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate profile %q", ProfilesFile, p.Name)
		}
		ps.byName[p.Name] = p
		ps.order = append(ps.order, p.Name)
	}
	sort.Strings(ps.order)
	return ps, nil
}

// All returns the profiles in stable name order.
func (ps *Profiles) All() []Profile {
	out := make([]Profile, 0, len(ps.order))
	for _, n := range ps.order {
		out = append(out, ps.byName[n])
	}
	return out
}

// Get returns one profile by name.
func (ps *Profiles) Get(name string) (Profile, bool) {
	p, ok := ps.byName[name]
	return p, ok
}

// Len is the number of known profiles.
func (ps *Profiles) Len() int { return len(ps.order) }

// ApplyProfile instantiates a profile as a regular policy plus (when the
// profile names a class) a class filter and an org-wide assignment.
// Re-applying refreshes the policy to the profile's current content - the
// drift-repair path. A hand-made policy occupying the profile's id is never
// clobbered, an existing filter of the derived name is never overwritten
// (it may be hand-tuned), and an assignment already in place is kept.
func ApplyProfile(p Profile) Mutation {
	return func(f *Fleet) error {
		if ex, ok := f.Policies[p.Name]; ok && !strings.HasPrefix(ex.Profile, p.Name+"@") {
			return fmt.Errorf("policy %q exists and did not come from profile %q; rename or remove it first", p.Name, p.Name)
		}
		pol := Policy{
			Name:        p.Label,
			Description: p.Description,
			Settings:    maps.Clone(p.Settings),
			Profile:     p.Provenance(),
		}
		if err := PutPolicy(p.Name, pol)(f); err != nil {
			return err
		}
		filter := ""
		if p.Class != "" {
			filter = "class-" + p.Class
			if _, ok := f.Filters[filter]; !ok {
				fl := Filter{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: p.Class}}}
				if err := PutFilter(filter, fl)(f); err != nil {
					return err
				}
			}
		}
		a := Assignment{Policy: p.Name, Target: "org", Filter: filter}
		for _, ex := range f.Assignments {
			if ex.Policy == a.Policy && ex.Target == a.Target && ex.Filter == a.Filter {
				return nil
			}
		}
		return Assign(a)(f)
	}
}
