package fleet

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// bundles.go: capability bundles. A bundle is an opinionated group of
// settings the console applies together at a scope - "turn on Identity" =
// SSSD login PLUS the matching hardening, with only the few real choices
// (the cache duration) left visible. Authored as overlay data
// (bundles.json), so DAWO ships its opinion and a tenant overrides it.
// Applying a bundle writes its settings through the normal gated settings
// transaction (like any save); it is a starting point, fully overridable
// afterwards. Distinct from a Profile (a per-device-class policy template):
// a bundle is a capability toggle at a scope.

// BundlesFile is the bundle file's path inside the overlay repo.
const BundlesFile = "bundles.json"

// Bundle is one capability bundle.
type Bundle struct {
	// Name is the slug identity.
	Name string `json:"name"`
	// Label is the human name the console shows ("Identity & login").
	Label string `json:"label,omitempty"`
	// Description explains the capability and what it turns on.
	Description string `json:"description,omitempty"`
	// Icon is an optional Material Symbols name for the card.
	Icon string `json:"icon,omitempty"`
	// Settings are the dawo.* values the bundle applies. Keys the operator
	// should not routinely see are applied but hidden; Exposed lists the ones
	// that stay visible as knobs on the card.
	Settings map[string]any `json:"settings"`
	// Exposed lists the setting keys that stay operator-visible (the knobs,
	// e.g. the offline-cache duration); every other key is applied silently.
	Exposed []string `json:"exposed,omitempty"`
	// Enable is the key whose truthiness marks the bundle "on" at a scope.
	// Empty falls back to the first ".enable" key in Settings.
	Enable string `json:"enable,omitempty"`
}

// EnableKey is the setting whose value marks the bundle on: the declared
// Enable, else the first ".enable" key in Settings (sorted for stability),
// else "".
func (b Bundle) EnableKey() string {
	if b.Enable != "" {
		return b.Enable
	}
	keys := make([]string, 0, len(b.Settings))
	for k := range b.Settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if strings.HasSuffix(k, ".enable") {
			return k
		}
	}
	return ""
}

// IsExposed reports whether a setting key is one of the bundle's visible
// knobs.
func (b Bundle) IsExposed(key string) bool {
	for _, e := range b.Exposed {
		if e == key {
			return true
		}
	}
	return false
}

// Bundles is the parsed, indexed bundle set.
type Bundles struct {
	byName map[string]Bundle
	order  []string
}

// ParseBundles reads bundles.json. Empty input yields an empty (never nil)
// set. A malformed document, a non-slug name, an empty settings map, an
// exposed key not in settings, or a duplicate is rejected so a broken bundle
// never renders as applyable.
func ParseBundles(raw []byte) (*Bundles, error) {
	bs := &Bundles{byName: map[string]Bundle{}}
	if len(raw) == 0 {
		return bs, nil
	}
	var list []Bundle
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("parse %s: %w", BundlesFile, err)
	}
	for _, b := range list {
		if !ValidateSlug(b.Name) {
			return nil, fmt.Errorf("%s: bundle name %q: must be a lowercase slug", BundlesFile, b.Name)
		}
		if len(b.Settings) == 0 {
			return nil, fmt.Errorf("%s: bundle %q has no settings", BundlesFile, b.Name)
		}
		for _, e := range b.Exposed {
			if _, ok := b.Settings[e]; !ok {
				return nil, fmt.Errorf("%s: bundle %q exposes %q but does not set it", BundlesFile, b.Name, e)
			}
		}
		if _, dup := bs.byName[b.Name]; dup {
			return nil, fmt.Errorf("%s: duplicate bundle %q", BundlesFile, b.Name)
		}
		bs.byName[b.Name] = b
		bs.order = append(bs.order, b.Name)
	}
	sort.Strings(bs.order)
	return bs, nil
}

// All returns the bundles in stable name order.
func (bs *Bundles) All() []Bundle {
	out := make([]Bundle, 0, len(bs.order))
	for _, n := range bs.order {
		out = append(out, bs.byName[n])
	}
	return out
}

// Get returns one bundle by name.
func (bs *Bundles) Get(name string) (Bundle, bool) {
	b, ok := bs.byName[name]
	return b, ok
}

// Len is the number of known bundles.
func (bs *Bundles) Len() int { return len(bs.order) }
