package web

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// bundle_ops.go: capability bundles on the settings page. A bundle applies a
// curated group of settings at a scope in one gated commit (the same write
// path as a normal save), turning on a capability plus its sensible
// companions while leaving a few knobs visible.

// exposedRow is one operator-visible knob of a bundle: the catalog entry plus
// its current effective value at the scope (own value, else the bundle's
// recommended default), rendered as an editable control on the card.
type exposedRow struct {
	Entry fleet.CatalogEntry
	Value string
	Set   bool
}

// bundleCard is a bundle rendered against the current scope.
type bundleCard struct {
	Name, Label, Description, Icon string
	On                             bool // the enable key is effective-on here
	Exposed                        []exposedRow
}

// bundleCards renders the overlay's bundles against a scope's settings.
func bundleCards(cat *fleet.Catalog, bundles *fleet.Bundles, own map[string]any, resolved map[string]fleet.Resolution) []bundleCard {
	cards := make([]bundleCard, 0, bundles.Len())
	for _, b := range bundles.All() {
		label := b.Label
		if label == "" {
			label = b.Name
		}
		card := bundleCard{Name: b.Name, Label: label, Description: b.Description, Icon: b.Icon}
		if ek := b.EnableKey(); ek != "" {
			card.On = effectiveBool(cat, own, resolved, ek)
		}
		for _, key := range b.Exposed {
			e, ok := cat.Lookup(key)
			if !ok {
				continue // an exposed key the catalog does not know: skip, do not crash
			}
			row := exposedRow{Entry: e}
			if v, has := own[key]; has {
				row.Set, row.Value = true, renderValue(v)
			} else {
				row.Value = renderValue(b.Settings[key]) // the bundle's recommendation
			}
			card.Exposed = append(card.Exposed, row)
		}
		cards = append(cards, card)
	}
	return cards
}

// postBundleApply applies a bundle at a scope: the bundle's settings, with any
// submitted exposed-knob values overriding the recommendation, written through
// the normal gated settings transaction. Editor at the scope.
func (s *Server) postBundleApply(w http.ResponseWriter, r *http.Request, v view) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	scope := r.FormValue("scope")
	if scope == "" {
		scope = "org"
	}
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	name := r.PathValue("name")
	b, ok := s.svc.Config.Bundles().Get(name)
	if !ok {
		return fmt.Errorf("unknown bundle %q", name)
	}
	cat := s.svc.Config.Catalog()

	// Build the change set from the bundle's settings; an exposed knob the
	// operator submitted (v:<key>) overrides the recommendation.
	var changes []app.SettingChange
	for key, val := range b.Settings {
		raw := renderValue(val)
		if b.IsExposed(key) {
			if sub, has := r.Form["v:"+key]; has && len(sub) > 0 {
				raw = sub[0]
			}
		}
		// Skip integration keys that live on their own page? No - a bundle may
		// legitimately turn on an integration; the gate validates either way.
		if _, known := cat.Lookup(key); !known {
			// A bundle referencing a key the catalog does not publish would
			// fail the gate; skip it with no silent effect rather than commit
			// an un-renderable setting.
			continue
		}
		changes = append(changes, app.SettingChange{Key: key, RawValue: raw})
	}
	if len(changes) == 0 {
		http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
		return nil
	}
	if err := app.GuardExclusiveSettings(s.svc.Config, scope, changes); err != nil {
		return err
	}
	if err := app.GuardBrickingSettings(r.Context(), s.svc.Config, s.svc.Inventory, scope, changes); err != nil {
		return err
	}
	author := webAuthor(v)
	desc := fmt.Sprintf("bundle: apply %s at %s", name, scopeLabel(scope))
	if err := s.runGated(r, v, desc, func(ctx context.Context) error {
		return s.svc.Config.ApplySettings(ctx, scope, changes, author)
	}); err != nil {
		if errors.Is(err, app.ErrChangeRequestRequired) && s.svc.Changes != nil {
			return s.stageSettingsAsChange(w, r, v, scope, changes)
		}
		return err
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}
