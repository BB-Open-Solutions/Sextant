package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// hardware.go is the Hardware page: one row per model the fleet runs, and the
// settings that follow that model.
//
// It adds no resolution rule (ADR 0027). Configuring a model writes a policy
// with a hardware filter and an assignment, which is what an operator could
// always have assembled by hand and nobody did, because nothing said it was
// possible and it took three edits in the right order.

// hardwareRow is one model on the page.
type hardwareRow struct {
	Name string
	// Vendor, Models and Notes come from the overlay's imaging catalog. Empty
	// for a model devices carry that the overlay has not described - that is
	// worth showing, not hiding: it means nothing can image one.
	Vendor  string
	Models  []string
	Notes   string
	Known   bool // described by the overlay's hardware-profiles.json
	Devices int
	// Configured, SettingsText and Target describe the model's own settings.
	Configured   bool
	SettingsText string
	EnforcedText string
	Target       string
}

func (s *Server) hardwarePage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	profiles := s.svc.Config.HardwareProfiles()
	counts := f.HardwareInUse()

	// Every model the overlay describes, plus every model devices actually
	// carry. The union, because either half alone lies: a catalogued model
	// with no devices is still configurable, and a model in use that the
	// catalog has never heard of is the more urgent of the two.
	names := map[string]bool{}
	for _, p := range profiles.All() {
		names[p.Name] = true
	}
	for name := range counts {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)

	rows := make([]hardwareRow, 0, len(ordered))
	for _, name := range ordered {
		row := hardwareRow{Name: name, Devices: counts[name], Target: "org"}
		if p, ok := profiles.Get(name); ok {
			row.Known, row.Vendor, row.Models, row.Notes = true, p.Vendor, p.Models, p.Notes
		}
		if pol, a, ok := f.HardwareConfig(name); ok {
			row.Configured = true
			var lines []string
			for _, k := range sortedKeys(pol.Settings) {
				lines = append(lines, fmt.Sprintf("%s = %s", k, renderValue(pol.Settings[k])))
			}
			row.SettingsText = strings.Join(lines, "\n")
			row.EnforcedText = strings.Join(pol.Enforced, ", ")
			if a.Target != "" {
				row.Target = a.Target
			}
		}
		rows = append(rows, row)
	}

	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)

	s.render(w, "hardware", map[string]any{
		"Title": "Hardware", "Nav": "hardware", "Rows": rows, "Groups": groups,
		"CanOwn": s.requireWeb(v, "org", identity.Owner) == nil,
	}, v)
}

// postHardwareConfigure writes one model's settings: the policy, the filter
// that selects exactly that model, and the assignment binding them, in one
// gated commit.
func (s *Server) postHardwareConfigure(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	// A blank payload is the removal path, not a malformed policy. Parsed
	// first, it fails with "policy needs at least one setting", which is true
	// of a policy and beside the point here: the operator asked for this
	// model to stop being configured.
	var settings map[string]any
	if raw := strings.TrimSpace(r.FormValue("settings")); raw != "" {
		parsed, err := s.parsePolicySettings(raw)
		if err != nil {
			return err
		}
		settings = parsed
	}
	var enforced []string
	for _, k := range strings.Split(r.FormValue("enforced"), ",") {
		if k = strings.TrimSpace(k); k != "" {
			enforced = append(enforced, k)
		}
	}
	target := strings.TrimSpace(r.FormValue("target"))
	if target == "" {
		target = "org"
	}
	msg := fmt.Sprintf("hardware: configure %s at %s", name, target)
	if len(settings) == 0 {
		msg = fmt.Sprintf("hardware: clear the configuration for %s", name)
	}
	if err := s.applyGated(r, v, fleet.ConfigureHardware(name, target, settings, enforced), msg); err != nil {
		return err
	}
	http.Redirect(w, r, "/hardware", http.StatusSeeOther)
	return nil
}
