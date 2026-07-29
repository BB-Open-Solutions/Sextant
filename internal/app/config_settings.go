package app

import (
	"context"
	"fmt"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// SetSetting validates and applies one catalog setting at a scope directly on
// main. It owns the invariants every transport must share, so neither the web
// console nor the JSON API can bypass them: change-request governance, catalog
// membership, typed parsing, secret-reference integrity, the affected-host set
// (which bounds gate validation) and the commit-message convention. rawValue is
// the value as entered - the catalog entry parses it to its typed form. enforce
// nil leaves the enforce state unchanged; non-nil locks (true) or unlocks it.
func (s *ConfigService) SetSetting(ctx context.Context, scope, key, rawValue string, enforce *bool, a ports.Author) error {
	if err := s.requireDirectEditAllowed(); err != nil {
		return err
	}
	entry, ok := s.Catalog().Lookup(key)
	if !ok {
		return fmt.Errorf("unknown setting %q (not in catalog)", key)
	}
	raw := strings.TrimSpace(rawValue)
	if raw == "" {
		return fmt.Errorf("no value chosen for %s; pick a value, or clear it to inherit", key)
	}
	val, err := entry.ParseValue(raw)
	if err != nil {
		return err
	}
	// A secret setting stores a REFERENCE; it must point at a secret the org has
	// registered, so a setting never dangles at a name that resolves to nothing
	// on the device.
	if entry.Widget() == fleet.WidgetSecret {
		if ref, _ := val.(string); ref != "" && !s.Fleet().HasSecretRef(ref) {
			return fmt.Errorf("unknown secret reference %q; register it first", ref)
		}
	}
	msg := fmt.Sprintf("settings: set %s at %s", key, scope)
	if enforce != nil && *enforce {
		msg += " (enforced)"
	}
	mut := func(f *fleet.Fleet) error {
		if err := fleet.SetScopeSetting(scope, key, val)(f); err != nil {
			return err
		}
		if enforce != nil {
			return fleet.SetScopeEnforce(scope, key, *enforce)(f)
		}
		return nil
	}
	return s.Apply(ctx, mut, msg, a, AffectedHosts(s.Fleet(), scope)...)
}

// ClearSetting reverts one setting at a scope to inherited, under the same
// change-request governance and affected-host scoping as SetSetting.
func (s *ConfigService) ClearSetting(ctx context.Context, scope, key string, a ports.Author) error {
	if err := s.requireDirectEditAllowed(); err != nil {
		return err
	}
	msg := fmt.Sprintf("settings: clear %s at %s", key, scope)
	return s.Apply(ctx, fleet.ClearScopeSetting(scope, key), msg, a, AffectedHosts(s.Fleet(), scope)...)
}

// SettingChange is one setting to set or clear in a batch save.
type SettingChange struct {
	Key      string
	RawValue string // value as entered; ignored when Clear is true
	Enforce  bool
	Clear    bool
}

// ApplySettings applies a batch of setting changes at one scope in a SINGLE
// gated commit - the console's save-all path. It owns the same invariants as
// SetSetting (governance, catalog membership, typing, secret-reference
// integrity) for every change, and validates them ALL before mutating, so one
// bad value rejects the whole save instead of half-applying it.
func (s *ConfigService) ApplySettings(ctx context.Context, scope string, changes []SettingChange, a ports.Author) error {
	return s.ApplySettingsMarked(ctx, scope, changes, "", a)
}

// ApplySettingsMarked is ApplySettings with marker appended to the commit
// SUBJECT. The console marks a high-risk save with " "+RiskHighMarker so the
// rollout engine's risk brake reads the risk back off the commit log (design
// 0012); the marker therefore has to survive into git, not merely into the
// console's own wording. An empty marker is exactly ApplySettings.
func (s *ConfigService) ApplySettingsMarked(ctx context.Context, scope string,
	changes []SettingChange, marker string, a ports.Author) error {
	if err := s.requireDirectEditAllowed(); err != nil {
		return err
	}
	if len(changes) == 0 {
		return nil
	}
	mut, msg, hosts, err := s.SettingsMutation(scope, changes)
	if err != nil {
		return err
	}
	return s.Apply(ctx, mut, msg+marker, a, hosts...)
}

// SettingsMutation compiles a batch of setting changes at one scope into a
// single mutation, plus its commit message and the hosts it affects. It
// validates every change - catalog membership, typing, secret-reference
// integrity - before any is applied, so one bad value rejects the whole batch.
// It touches no git: ApplySettings applies it to main directly, while the web
// layer stages the same mutation on a change request's branch when the org
// mandates review (so a review-gated save flows into the review process instead
// of failing).
func (s *ConfigService) SettingsMutation(scope string, changes []SettingChange) (fleet.Mutation, string, []string, error) {
	muts := make([]fleet.Mutation, 0, len(changes))
	for _, c := range changes {
		if c.Clear {
			muts = append(muts, fleet.ClearScopeSetting(scope, c.Key))
			continue
		}
		entry, ok := s.Catalog().Lookup(c.Key)
		if !ok {
			return nil, "", nil, fmt.Errorf("unknown setting %q (not in catalog)", c.Key)
		}
		val, err := entry.ParseValue(strings.TrimSpace(c.RawValue))
		if err != nil {
			return nil, "", nil, err
		}
		if entry.Widget() == fleet.WidgetSecret {
			if ref, _ := val.(string); ref != "" && !s.Fleet().HasSecretRef(ref) {
				return nil, "", nil, fmt.Errorf("unknown secret reference %q; register it first", ref)
			}
		}
		key, enforce := c.Key, c.Enforce
		muts = append(muts, func(f *fleet.Fleet) error {
			if err := fleet.SetScopeSetting(scope, key, val)(f); err != nil {
				return err
			}
			return fleet.SetScopeEnforce(scope, key, enforce)(f)
		})
	}
	combined := func(f *fleet.Fleet) error {
		for _, m := range muts {
			if err := m(f); err != nil {
				return err
			}
		}
		return nil
	}
	msg := fmt.Sprintf("settings: update %d at %s", len(changes), scope)
	return combined, msg, AffectedHosts(s.Fleet(), scope), nil
}

// requireDirectEditAllowed rejects a direct-to-main edit when the org mandates
// that configuration changes flow through a reviewed change request. The change
// flow itself edits on a branch (ChangeService), not through this path, so it
// is never blocked.
func (s *ConfigService) requireDirectEditAllowed() error {
	if a := s.Fleet().Assurance; a != nil && a.RequireChangeRequest {
		return ErrChangeRequestRequired
	}
	return nil
}
