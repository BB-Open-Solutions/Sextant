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
	Entry fleet.CatalogEntry
	Set   bool   // a value exists at exactly this scope
	Value string // that value, rendered
	Lines string // list values, one item per line, for the code editor
	// RangeFrom/RangeTo split a "HH:MM-HH:MM" value across the two time
	// inputs of the timerange widget. Empty when unset - the inputs then show
	// nothing rather than a guess.
	RangeFrom, RangeTo string
	// Slots are the placeholder texts for a fixedlist widget (the declared
	// defaults, plus one spare), and SlotValues the values actually set at
	// this scope, index-aligned. Defaults are placeholders and never values:
	// prefilling them would turn "inherits" into an explicit write the moment
	// somebody saves the form for an unrelated reason.
	Slots       []string
	SlotValues  []string
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
	// ImageTime marks an option that is written into the image rather than
	// applied to a running device (see imageTimeKey), so the editor says when
	// it lands instead of implying a live ceremony.
	ImageTime bool
	// GovernedBy names the policies that already carry this key at or above
	// this scope, and GovernedLocked says one of them locks it (ADR 0017).
	// A key under governance is not a free local choice: setting it here
	// either competes with a policy that records WHY the value is what it is,
	// or - when locked - does nothing at all. The editor has to say which,
	// because an input that accepts a value it will never apply is the one
	// thing worse than no input.
	GovernedBy     []string
	GovernedLocked bool
	// PolicyOnly marks a control that may only be set through a policy
	// (ADR 0017). Such a row appears here only when this scope still carries
	// an inline value, and then read-only: the value is left applying and the
	// row says where it belongs, rather than vanishing and silently ceasing
	// to have an effect.
	PolicyOnly bool
}

// appsShown caps how many names an app list prints before it says "N more".
// Ten is what fits on one line at the widths this page uses, and the point of
// the summary is that it can be read at a glance rather than dragged through.
const appsShown = 10

// appsRow prepares one app list for the settings page: what to print, how many
// are left over, and the whole list one name per line for the edit window.
// Splitting it here rather than in the template keeps arithmetic out of the
// markup - a template that can count is a template that can be wrong quietly.
func appsRow(kind string, names []string) map[string]any {
	shown, rest := names, 0
	if len(names) > appsShown {
		shown, rest = names[:appsShown], len(names)-appsShown
	}
	return map[string]any{
		"Kind": kind, "Shown": shown, "Rest": rest, "Count": len(names),
		"Text": strings.Join(names, "\n"),
	}
}

// imageTimePrefixes are the dotted namespaces whose options only take hold
// when a device is (re)imaged: Secure Boot key enrollment and TPM2 disk
// unlock are decided at install time (design 0001, decision 2026-07-28).
// app.KeySecureBoot and app.KeyTPM2 live under them - imageTimeKey is asserted
// against both, so a rename of either shows up as a failing test, not as a
// silently missing hint.
var imageTimePrefixes = []string{"secureboot.", "diskUnlock."}

// imageTimeKey reports whether a setting is image-time rather than live.
func imageTimeKey(key string) bool {
	for _, p := range imageTimePrefixes {
		if strings.HasPrefix(key, p) {
			return true
		}
	}
	return false
}

// riskClassHigh is the catalog's convention for "changing this deserves extra
// care" (fleet.CatalogEntry.RiskClass).
const riskClassHigh = "high"

// riskMarkerFor decides whether a save deserves a human at the wheel and
// returns the suffix its commit subject carries when it does (" " +
// app.RiskHighMarker, empty otherwise): the rollout engine holds auto-flow
// behind a marked commit until an operator dispatches the run (design 0012).
// Two classes qualify - options the catalog marks riskClass "high", and an
// integration being switched on or off (netbird.enable and friends: the device
// joins or leaves a mesh, which is not something to discover from a wave).
// Image-time keys never qualify, even when the catalog marks them high:
// secureboot./diskUnlock. options are written into the image and stay inert
// until a device is re-imaged (design 0001), so flowing them changes nothing
// on a running fleet and braking on them would hold the whole save hostage.
func riskMarkerFor(cat *fleet.Catalog, changes []app.SettingChange) string {
	for _, c := range changes {
		if imageTimeKey(c.Key) {
			continue
		}
		if e, ok := cat.Lookup(c.Key); ok && e.RiskClass == riskClassHigh {
			return " " + app.RiskHighMarker
		}
		if isIntegrationSetting(c.Key) && strings.HasSuffix(c.Key, ".enable") {
			return " " + app.RiskHighMarker
		}
	}
	return ""
}

// requiresOf finds the enable an option depends on: the longest dotted prefix
// q of key for which q+".enable" is a catalog option. Convention over
// metadata - the exported module tree already encodes the relation. It
// delegates to the catalog so the page's idea of "what gates this" and the
// ordering that puts a gate first can never drift apart.
func requiresOf(cat *fleet.Catalog, key string) string { return cat.Requires(key) }

// combineSubmitted turns one setting's submitted form fields into the single
// raw string ParseValue expects.
//
// Most widgets are one control and one value. Two are not: a maintenance
// window is a from and a to, and a fixed list is one input per slot. Both post
// several fields under the SAME name, and the combining rule belongs here
// rather than in each template - the parser is the one that decides what a
// value looks like, so exactly one place should assemble it.
func combineSubmitted(e fleet.CatalogEntry, vals []string) string {
	trimmed := make([]string, 0, len(vals))
	for _, v := range vals {
		trimmed = append(trimmed, strings.TrimSpace(v))
	}
	switch e.Widget() {
	case fleet.WidgetTimeRange:
		// Two halves. Either both or neither: half a window is not a window,
		// and "09:00-" would reach the device as an unparseable range.
		if len(trimmed) < 2 || trimmed[0] == "" || trimmed[1] == "" {
			return ""
		}
		return trimmed[0] + "-" + trimmed[1]
	case fleet.WidgetFixedList:
		// One item per slot, blanks dropped - the same one-per-line form the
		// list parser already takes, so an empty slot is simply not an item.
		out := make([]string, 0, len(trimmed))
		for _, v := range trimmed {
			if v != "" {
				out = append(out, v)
			}
		}
		return strings.Join(out, "\n")
	default:
		if len(trimmed) == 0 {
			return ""
		}
		return trimmed[0]
	}
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
	governed := f.Governors(scope)

	var sections []settingSection
	for _, name := range cat.Categories() {
		sec := settingSection{Name: name}
		for _, e := range cat.ByCategory(name) {
			// Integration options live on the Integrations page (one card
			// each), not in the general settings editor - one key, one place.
			if isIntegrationSetting(e.Name) {
				continue
			}
			// Policy-only controls (ADR 0017) are not offered here. They are
			// hidden rather than disabled UNLESS this scope already carries an
			// inline value: silently dropping a key somebody set would stop it
			// applying with nothing on screen saying so, which is a worse
			// failure than the one this rule exists to prevent. An existing
			// value is shown, read-only, with where it now belongs.
			_, setHere := own[e.Name]
			if isPolicyOnly(e.Name) && !setHere {
				continue
			}
			row := settingRow{Entry: e, Enforced: locked[e.Name], Suggestions: textSuggestions[e.Name],
				ImageTime: imageTimeKey(e.Name), PolicyOnly: isPolicyOnly(e.Name)}
			if g, ok := governed[e.Name]; ok {
				row.GovernedBy, row.GovernedLocked = g.Names, g.Enforced
				for i, n := range row.GovernedBy {
					if n == "" { // a policy with no human name shows as its id
						row.GovernedBy[i] = g.Policies[i]
					}
				}
			}
			if req := requiresOf(cat, e.Name); req != "" {
				row.RequiresKey = req
				row.RequiresOff = !effectiveBool(cat, own, resolved, req)
				row.RequiresInherited = effectiveBool(cat, nil, resolved, req)
			}
			if val, has := own[e.Name]; has {
				row.Set, row.Value = true, renderValue(val)
				row.Lines = valueLines(val)
			}
			switch e.Widget() {
			case fleet.WidgetTimeRange:
				row.RangeFrom, row.RangeTo = splitRange(row.Value)
			case fleet.WidgetFixedList:
				row.Slots, row.SlotValues = listSlots(e, row.Lines)
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
		// The page puts the picker in a "you are editing" strip, so the picker
		// must not repeat the word Scope inside it.
		"PickerLabelled": true,
		"Title":          "Settings", "Nav": "settings",
		"Scope": scope, "ScopeLabel": scopeLabel(scope),
		"Groups": groups, "Devices": devices, "SelGroup": selGroup, "Sections": sections,
		"Bundles":    bundleCards(cat, s.svc.Config.Bundles(), own, resolved),
		"SecretRefs": secretRefs,
		"IsDevice":   strings.HasPrefix(scope, "device:"),
		"Empty":      len(cat.Entries) == 0,
		"CanEdit":    v.roleAt(scope).Meets(identity.Editor),
		"Apps": []map[string]any{
			appsRow("packages", pkgs), appsRow("flatpaks", flats), appsRow("overlays", ovs),
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
		submitted := combineSubmitted(e, vals)
		enf := r.FormValue("e:"+e.Name) != ""
		curVal, curSet := own[e.Name]
		// A policy-only control (ADR 0017) may not gain a value here. Clearing
		// one IS allowed and is the point: that is how somebody moves it into
		// a policy - set it there, remove it here - and refusing the clear
		// would leave the inline value stranded forever.
		if isPolicyOnly(e.Name) && strings.TrimSpace(firstOf(vals)) != "" {
			if !curSet || renderValue(curVal) != strings.TrimSpace(firstOf(vals)) {
				return &webForbidden{e.Name + " is set through a policy, not here"}
			}
		}
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
	// detached: the save's nix validation outlived the grace window and runs
	// on in the background; the landing page then says "validating" (pending=1)
	// instead of silently showing pre-write values.
	var detached bool
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
		// The risk brake (design 0012): a high-risk save carries its marker into
		// the commit subject, where the rollout engine reads it and holds
		// auto-flow for an operator-dispatched run. The same marker rides the
		// save's own description, so the operator sees why it will wait.
		marker := riskMarkerFor(cat, changes)
		desc := fmt.Sprintf("settings: %d change(s) at %s%s", len(changes), scopeLabel(scope), marker)
		if det, err := s.runGatedDetached(r, v, desc, func(ctx context.Context) error {
			return s.svc.Config.ApplySettingsMarked(ctx, scope, changes, marker, author)
		}); err != nil {
			// A review-gated org does not fail the save: it flows into the review
			// process. Stage the same edits on a fresh change request and send the
			// operator to the Updates board.
			if errors.Is(err, app.ErrChangeRequestRequired) && s.svc.Changes != nil {
				return s.stageSettingsAsChange(w, r, v, scope, changes, marker)
			}
			return err
		} else {
			detached = det
		}
	}
	// A save must land back where it was made: the integrations cards post
	// to this same handler and losing the operator to the settings editor
	// read as a broken save (operator report 2026-07-29). Local paths only,
	// same open-redirect guard as the secret-reveal back link.
	if back := r.FormValue("back"); strings.HasPrefix(back, "/") && !strings.HasPrefix(back, "//") {
		// pending=1 lets the landing page say "validating" and refresh once,
		// instead of silently showing pre-write values while the background
		// eval runs (operator report 2026-07-29).
		if detached {
			sep := "?"
			if strings.Contains(back, "?") {
				sep = "&"
			}
			back += sep + "pending=1"
		}
		http.Redirect(w, r, back, http.StatusSeeOther)
		return nil
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}

// stageSettingsAsChange opens a fresh change request, stages the batch of edits
// on its branch, and redirects to the Updates board - the review path a save
// takes when the organisation mandates a change request. marker (riskMarkerFor)
// travels on the staged commit too: once the change merges, the same brake
// applies to the same edits.
func (s *Server) stageSettingsAsChange(w http.ResponseWriter, r *http.Request, v view,
	scope string, changes []app.SettingChange, marker string) error {
	mut, msg, hosts, err := s.svc.Config.SettingsMutation(scope, changes)
	if err != nil {
		return err
	}
	msg += marker
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

// firstOf is the first submitted value for a form key, or "" when the field
// was present but empty.
func firstOf(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// splitRange splits a stored "HH:MM-HH:MM" into the two time inputs. A value
// that is not a range yields two empty fields rather than a half-parsed one:
// the operator then sees an empty control and sets it, instead of a field
// silently holding something the parser will reject.
func splitRange(v string) (from, to string) {
	f, t, ok := strings.Cut(v, "-")
	if !ok {
		return "", ""
	}
	return strings.TrimSpace(f), strings.TrimSpace(t)
}

// listSlots builds the fixed-list controls: one slot per declared default
// plus a spare, and at least four so there is room to add without the field
// count changing under the operator.
//
// The defaults are returned as PLACEHOLDERS (slots) and the set values
// separately (values). Prefilling the defaults as values would mean that
// saving the page - for any reason, including an unrelated setting - writes
// them explicitly at this scope and moves the row from "inherits" to
// "modified here". The provenance this page shows is the point of it.
func listSlots(e fleet.CatalogEntry, lines string) (slots, values []string) {
	if d, ok := e.Default.([]any); ok {
		for _, v := range d {
			if s, ok := v.(string); ok {
				slots = append(slots, s)
			}
		}
	}
	slots = append(slots, "") // a spare, so adding one needs no second save
	for len(slots) < 4 {
		slots = append(slots, "")
	}
	values = make([]string, len(slots))
	if lines != "" {
		for i, v := range strings.Split(lines, "\n") {
			if i < len(values) {
				values[i] = v
			}
		}
	}
	return slots, values
}
