package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// settings.go renders the catalog-driven settings editor (ADR 0005): the
// entire form surface derives from catalog.json in the overlay repo. No
// option is hand-coded here; a new documented dawo.* option appears by
// itself once the overlay re-exports its catalog.

// settingRow is one catalog entry joined with the scope's current state.
type settingRow struct {
	Entry    fleet.CatalogEntry
	Set      bool   // a value exists at exactly this scope
	Value    string // that value, rendered
	Enforced bool
	Resolved string // device scope only: effective value after the chain
	Source   string // device scope only: which scope/policy won
}

// settingSection groups rows per catalog category.
type settingSection struct {
	Name string
	Rows []settingRow
}

// settingsPage renders the editor for one scope (?scope=org|group:x|device:y).
func (s *Server) settingsPage(w http.ResponseWriter, r *http.Request, v view) {
	// One snapshot for fleet AND catalog: separate loads could join a fleet
	// from one revision with the vocabulary of another mid-reload.
	f, cat := s.svc.Config.Snapshot()
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "org"
	}
	own, enforced, err := f.ScopeSettings(scope)
	// An invisible scope answers exactly like a missing one. Org is the
	// exception: it is every user's root, its own settings are part of
	// their devices' effective config anyway.
	if err != nil || (scope != "org" && !v.canView(scope)) {
		http.NotFound(w, r)
		return
	}
	locked := map[string]bool{}
	for _, k := range enforced {
		locked[k] = true
	}
	var resolved map[string]fleet.Resolution
	if tag, ok := strings.CutPrefix(scope, "device:"); ok {
		resolved = f.Resolve(tag)
	}

	var sections []settingSection
	for _, name := range cat.Categories() {
		sec := settingSection{Name: name}
		for _, e := range cat.ByCategory(name) {
			row := settingRow{Entry: e, Enforced: locked[e.Name]}
			if val, has := own[e.Name]; has {
				row.Set, row.Value = true, renderValue(val)
			}
			if res, has := resolved[e.Name]; has {
				row.Resolved, row.Source = renderValue(res.Value), res.Source.String()
			}
			sec.Rows = append(sec.Rows, row)
		}
		sections = append(sections, sec)
	}

	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		if v.canView("group:" + g) {
			groups = append(groups, g)
		}
	}
	sort.Strings(groups)

	// Registered secret-reference names, for the secret-widget picker.
	secretRefs := make([]string, 0, len(f.SecretRefs))
	for name := range f.SecretRefs {
		secretRefs = append(secretRefs, name)
	}
	sort.Strings(secretRefs)

	// The scope's own app lists (additive across the chain; edited here).
	var pkgs, flats, ovs []string
	switch {
	case scope == "org":
		if f.Org != nil {
			pkgs, flats, ovs = f.Org.Packages, f.Org.Flatpaks, f.Org.Overlays
		}
	case strings.HasPrefix(scope, "group:"):
		g := f.Groups[strings.TrimPrefix(scope, "group:")]
		pkgs, flats, ovs = g.Packages, g.Flatpaks, g.Overlays
	case strings.HasPrefix(scope, "device:"):
		d := f.Devices[strings.TrimPrefix(scope, "device:")]
		pkgs, flats, ovs = d.Packages, d.Flatpaks, d.Overlays
	}

	s.render(w, "settings", map[string]any{
		"Title": "Settings", "Nav": "settings",
		"Scope": scope, "Groups": groups, "Sections": sections,
		"SecretRefs": secretRefs,
		"IsDevice":   strings.HasPrefix(scope, "device:"),
		"Empty":      len(cat.Entries) == 0,
		"CanEdit":    v.roleAt(scope).Meets(identity.Editor),
		"Apps": []map[string]string{
			{"Kind": "packages", "Names": strings.Join(pkgs, ", ")},
			{"Kind": "flatpaks", "Names": strings.Join(flats, ", ")},
			{"Kind": "overlays", "Names": strings.Join(ovs, ", ")},
		},
	}, v)
}

// postSetting applies one editor action: set (value + enforce state) or
// clear. The key must exist in the catalog - this page only speaks the
// documented vocabulary; free-form keys go through the API or device page.
func (s *Server) postSetting(w http.ResponseWriter, r *http.Request, v view) error {
	scope := r.FormValue("scope")
	key := r.FormValue("key")
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	entry, ok := s.svc.Config.Catalog().Lookup(key)
	if !ok {
		return fmt.Errorf("unknown setting %q (not in catalog)", key)
	}

	var mut fleet.Mutation
	var msg string
	switch action := r.FormValue("action"); action {
	case "clear":
		mut = fleet.ClearScopeSetting(scope, key)
		msg = fmt.Sprintf("settings: clear %s at %s", key, scope)
	case "set":
		raw := strings.TrimSpace(r.FormValue("value"))
		// An untouched widget submits "" (the inherit placeholder). Never
		// coerce that into a real value - a bare Apply (e.g. to flip the
		// enforce checkbox) must not silently pin false or "".
		if raw == "" {
			return fmt.Errorf("no value chosen for %s; pick a value, or use Clear to inherit", key)
		}
		val, err := entry.ParseValue(raw)
		if err != nil {
			return err
		}
		// A secret setting stores a reference name; it must point at a secret
		// the org has registered, so a setting never dangles at a name that
		// resolves to nothing on the device.
		if entry.Widget() == fleet.WidgetSecret {
			if ref, _ := val.(string); ref != "" && !s.svc.Config.Fleet().HasSecretRef(ref) {
				return fmt.Errorf("unknown secret reference %q; register it first", ref)
			}
		}
		// The checkbox is authoritative: set-with-enforce locks, plain set
		// unlocks a previously enforced key.
		enforce := r.FormValue("enforce") != ""
		mut = func(f *fleet.Fleet) error {
			if err := fleet.SetScopeSetting(scope, key, val)(f); err != nil {
				return err
			}
			return fleet.SetScopeEnforce(scope, key, enforce)(f)
		}
		msg = fmt.Sprintf("settings: set %s at %s", key, scope)
		if enforce {
			msg += " (enforced)"
		}
	default:
		return fmt.Errorf("unknown action %q", action)
	}

	if err := s.svc.Config.Apply(r.Context(), mut, msg, webAuthor(v),
		app.AffectedHosts(s.svc.Config.Fleet(), scope)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}

// renderValue displays a settings value compactly (JSON keeps strings
// distinguishable from numbers and booleans).
func renderValue(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}
