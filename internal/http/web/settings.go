package web

import (
	"context"
	"encoding/json"
	"errors"
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
	Entry       fleet.CatalogEntry
	Set         bool   // a value exists at exactly this scope
	Value       string // that value, rendered
	Lines       string // list values, one item per line, for the code editor
	Enforced    bool
	Resolved    string // device scope only: effective value after the chain
	Source      string // device scope only: which scope/policy won
	Suggestions []string
	// RequiresKey names the enable this option depends on (the "<prefix>.
	// enable" convention: timesync.options.servers needs timesync.enable);
	// RequiresOff marks it currently off at this scope's best knowledge, so
	// the editor greys the control and says "takes effect once X is on"
	// instead of the value being silently inert (product-stability
	// principle: inert is fine, invisible is not). RequiresInherited is the
	// enable's value with any own edit ignored - the state the dependent
	// field falls back to when the editor puts the enable on "inherit"
	// (app.js re-greys live without a save round-trip).
	RequiresKey       string
	RequiresOff       bool
	RequiresInherited bool
}

// requiresOf finds the enable an option depends on: the longest dotted
// prefix q of key for which q+".enable" is a catalog option. Convention
// over metadata - the exported module tree already encodes the relation.
func requiresOf(cat *fleet.Catalog, key string) string {
	if strings.HasSuffix(key, ".enable") {
		return ""
	}
	parts := strings.Split(key, ".")
	for i := len(parts) - 1; i >= 1; i-- {
		q := strings.Join(parts[:i], ".") + ".enable"
		if _, ok := cat.Lookup(q); ok {
			return q
		}
	}
	return ""
}

// textSuggestions seeds a <datalist> of known-good values for a handful of
// free-text settings, so the operator gets a searchable dropdown without
// giving up the ability to type a custom value. The catalog itself has no
// notion of "known values" for a text-typed option (only "one of ..." enums
// render as WidgetSelect, ADR 0005) - extending that would mean teaching the
// overlay's nix option declarations about UI hints, which is out of scope
// here. This map is a small, hand-maintained seed; add entries as more
// commonly-copied URLs/paths come up. It intentionally stays tiny rather
// than growing into a generic suggestion system.
var textSuggestions = map[string][]string{
	"autoUpdate.options.repoUrl": {
		"https://code.overheid.nl/MinBZK/DAWO-NixOS.git",
	},
	// LUKS mapper names the disko layouts actually create; free typing here
	// bricks unlock on the next boot, so the known-good value leads.
	"diskUnlock.tpm2.device": {
		"crypted-main",
	},
}

// effectiveBool is this scope's best knowledge of a boolean option: the
// value set here, else the device-resolved value (device scope only), else
// the catalog default. Org/group scopes cannot see a deeper override, which
// only makes the hint conservative, never wrong-and-silent.
func effectiveBool(cat *fleet.Catalog, own map[string]any, resolved map[string]fleet.Resolution, key string) bool {
	if v, ok := own[key]; ok {
		b, _ := v.(bool)
		return b
	}
	if res, ok := resolved[key]; ok {
		b, _ := res.Value.(bool)
		return b
	}
	if e, ok := cat.Lookup(key); ok {
		b, _ := e.Default.(bool)
		return b
	}
	return false
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
			// Integration options live on the Integrations page (one card
			// each), not in the general settings editor - one key, one place.
			if isIntegrationSetting(e.Name) {
				continue
			}
			row := settingRow{Entry: e, Enforced: locked[e.Name], Suggestions: textSuggestions[e.Name]}
			if req := requiresOf(cat, e.Name); req != "" {
				row.RequiresKey = req
				row.RequiresOff = !effectiveBool(cat, own, resolved, req)
				row.RequiresInherited = effectiveBool(cat, nil, resolved, req)
			}
			if val, has := own[e.Name]; has {
				row.Set, row.Value = true, renderValue(val)
				row.Lines = valueLines(val)
			}
			if res, has := resolved[e.Name]; has {
				row.Resolved, row.Source = renderValue(res.Value), res.Source.String()
			}
			sec.Rows = append(sec.Rows, row)
		}
		// A category whose every entry moved to the Integrations page would
		// render as an empty card ("Netbird - 0 keys"): skip it.
		if len(sec.Rows) > 0 {
			sections = append(sections, sec)
		}
	}

	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		if v.canView("group:" + g) {
			groups = append(groups, g)
		}
	}
	sort.Strings(groups)

	// Scope selector cascade: organisation -> group (default "all") -> device
	// (default "all"). The selected group is the group in scope, or the group a
	// device in scope belongs to; "" means all groups (organisation level).
	selGroup := ""
	if g, ok := strings.CutPrefix(scope, "group:"); ok {
		selGroup = g
	} else if tag, ok := strings.CutPrefix(scope, "device:"); ok {
		if d, ok := f.Devices[tag]; ok && len(d.Groups) > 0 {
			selGroup = d.Groups[0]
		}
	}

	// Devices for the drill-down: filtered to the selected group, or all
	// viewable devices when no group is selected. Selecting one edits it.
	devices := make([]string, 0, len(f.Devices))
	for tag, d := range f.Devices {
		if !v.canView("device:" + tag) {
			continue
		}
		if selGroup != "" && !deviceInGroup(d, selGroup) {
			continue
		}
		devices = append(devices, tag)
	}
	sort.Strings(devices)

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
		"PickerBase": "/settings",
		"Title":      "Settings", "Nav": "settings",
		"Scope": scope, "ScopeLabel": scopeLabel(scope),
		"Groups": groups, "Devices": devices, "SelGroup": selGroup, "Sections": sections,
		"Bundles":    bundleCards(cat, s.svc.Config.Bundles(), own, resolved),
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

// postSetting saves a whole scope's editable settings in one gated commit: the
// operator edits several rows, then Save submits them all. Each row carries a
// value (v:<key>) and enforce flag (e:<key>); the handler diffs the submitted
// state against the scope's own settings and applies only what changed, so an
// unchanged Save is a no-op rather than an empty commit. All validation and
// governance live in ConfigService.ApplySettings.
func (s *Server) postSetting(w http.ResponseWriter, r *http.Request, v view) error {
	if err := r.ParseForm(); err != nil {
		return err
	}
	scope := r.FormValue("scope")
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	f, cat := s.svc.Config.Snapshot()
	own, enforced, err := f.ScopeSettings(scope)
	if err != nil {
		return err
	}
	locked := map[string]bool{}
	for _, k := range enforced {
		locked[k] = true
	}

	var changes []app.SettingChange
	for _, e := range cat.Entries {
		// Only keys PRESENT in the form take part in the diff: the settings
		// page resubmits every row, but a per-card form (the integrations
		// page) posts just its own keys - an absent field means "not on this
		// form", never "clear the value".
		vals, present := r.PostForm["v:"+e.Name]
		if !present {
			continue
		}
		submitted := ""
		if len(vals) > 0 {
			submitted = strings.TrimSpace(vals[0])
		}
		enf := r.FormValue("e:"+e.Name) != ""
		curVal, curSet := own[e.Name]
		curStr := ""
		if curSet {
			curStr = renderValue(curVal)
		}
		switch {
		case submitted == "" && curSet:
			changes = append(changes, app.SettingChange{Key: e.Name, Clear: true})
		case submitted != "" && (!curSet || submitted != curStr || enf != locked[e.Name]):
			changes = append(changes, app.SettingChange{Key: e.Name, RawValue: submitted, Enforce: enf})
		}
	}
	if len(changes) > 0 {
		// Brick guard: never let a save disable Secure Boot for a device
		// whose firmware still enforces it (settings_guard.go).
		if err := app.GuardExclusiveSettings(s.svc.Config, scope, changes); err != nil {
			return err
		}
		if err := app.GuardBrickingSettings(r.Context(), s.svc.Config, s.svc.Inventory, scope, changes); err != nil {
			return err
		}
		// Grace-window save: the change-request governance check fires in
		// milliseconds (well inside the window), so ErrChangeRequestRequired
		// still stages inline below; only the nix validation can detach.
		author := webAuthor(v)
		desc := fmt.Sprintf("settings: %d change(s) at %s", len(changes), scopeLabel(scope))
		if err := s.runGated(r, v, desc, func(ctx context.Context) error {
			return s.svc.Config.ApplySettings(ctx, scope, changes, author)
		}); err != nil {
			// A review-gated org does not fail the save: it flows into the review
			// process. Stage the same edits on a fresh change request and send the
			// operator to the Updates board.
			if errors.Is(err, app.ErrChangeRequestRequired) && s.svc.Changes != nil {
				return s.stageSettingsAsChange(w, r, v, scope, changes)
			}
			return err
		}
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}

// stageSettingsAsChange opens a fresh change request, stages the batch of edits
// on its branch, and redirects to the Updates board - the review path a save
// takes when the organisation mandates a change request.
func (s *Server) stageSettingsAsChange(w http.ResponseWriter, r *http.Request, v view, scope string, changes []app.SettingChange) error {
	mut, msg, hosts, err := s.svc.Config.SettingsMutation(scope, changes)
	if err != nil {
		return err
	}
	id, err := s.nextChangeID(r.Context(), scope)
	if err != nil {
		return err
	}
	title := fmt.Sprintf("Settings: %d change(s) at %s", len(changes), scopeLabel(scope))
	if _, err := s.svc.Changes.Open(r.Context(), id, title, webAuthor(v)); err != nil {
		return err
	}
	if err := s.svc.Changes.Edit(r.Context(), id, mut, msg, webAuthor(v), hosts...); err != nil {
		// Do not strand a freshly-opened CR when its edit fails.
		_, _ = s.svc.Changes.Abandon(r.Context(), id)
		return err
	}
	// A stale read snapshot can compute phantom changes (values the operator
	// submitted that main already holds): the staged branch then has no diff.
	// Do not leave an empty draft CR behind - abandon it and return to the
	// editor, where the desired state is already shown.
	if diff, derr := s.svc.Changes.Diff(r.Context(), id); derr == nil && strings.TrimSpace(diff) == "" {
		if _, err := s.svc.Changes.Abandon(r.Context(), id); err != nil {
			s.log.Warn("abandon empty settings CR", "id", id, "err", err)
		}
		http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
		return nil
	}
	http.Redirect(w, r, "/updates", http.StatusSeeOther)
	return nil
}

// nextChangeID mints a unique, valid change-request id for an auto-staged save.
func (s *Server) nextChangeID(ctx context.Context, scope string) (string, error) {
	list, err := s.svc.Changes.List(ctx)
	if err != nil {
		return "", err
	}
	existing := make(map[string]bool, len(list))
	for _, cr := range list {
		existing[cr.ID] = true
	}
	base := slugify("cfg-" + scope)
	for n := len(list) + 1; ; n++ {
		id := fmt.Sprintf("%s-%d", base, n)
		if !existing[id] {
			return id, nil
		}
	}
}

// deviceInGroup reports whether a device is a direct member of the group.
func deviceInGroup(d fleet.Device, group string) bool {
	for _, g := range d.Groups {
		if g == group {
			return true
		}
	}
	return false
}

// scopeLabel is the human name of the scope being edited, for a clear
// "Editing ..." indicator above the editor.
func scopeLabel(scope string) string {
	if g, ok := strings.CutPrefix(scope, "group:"); ok {
		return "group " + g
	}
	if d, ok := strings.CutPrefix(scope, "device:"); ok {
		return "device " + d
	}
	return "the organisation"
}

// valueLines renders a list-valued setting as one item per line, so the code
// editor shows it the way it is edited. Non-list values yield "".
func valueLines(v any) string {
	items, ok := v.([]any)
	if !ok {
		return ""
	}
	parts := make([]string, 0, len(items))
	for _, it := range items {
		if s, ok := it.(string); ok {
			parts = append(parts, s)
		} else {
			parts = append(parts, fmt.Sprintf("%v", it))
		}
	}
	return strings.Join(parts, "\n")
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
