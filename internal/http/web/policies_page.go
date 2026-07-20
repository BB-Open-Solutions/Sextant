package web

import (
	"fmt"
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

func (s *Server) policies(w http.ResponseWriter, _ *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	profiles := s.svc.Config.Profiles()
	type prow struct {
		ID, Description string
		Settings        map[string]any
		SettingsText    string // editable key = value form
		Enforced        []string
		EnforcedText    string
		Assignments     []fleet.Assignment
		Profile         string // source profile name, "" for hand-made
		Drift           bool   // the overlay's profile moved past this stamp
	}
	rows := make([]prow, 0, len(f.Policies))
	for _, id := range sortedKeys(f.Policies) {
		p := f.Policies[id]
		var asn []fleet.Assignment
		for _, a := range f.Assignments {
			if a.Policy == id {
				asn = append(asn, a)
			}
		}
		var lines []string
		for _, k := range sortedKeys(p.Settings) {
			lines = append(lines, fmt.Sprintf("%s = %s", k, renderValue(p.Settings[k])))
		}
		row := prow{ID: id, Description: p.Description,
			Settings: p.Settings, SettingsText: strings.Join(lines, "\n"),
			Enforced: p.Enforced, EnforcedText: strings.Join(p.Enforced, ", "),
			Assignments: asn}
		if name, _, ok := strings.Cut(p.Profile, "@"); ok {
			row.Profile = name
			if src, has := profiles.Get(name); has {
				row.Drift = src.Provenance() != p.Profile
			}
		}
		rows = append(rows, row)
	}
	// The overlay's recommended profiles, each with its instantiation state:
	// apply (never instantiated), reapply (drifted behind the overlay),
	// current (matches), or conflict (a hand-made policy owns the id).
	type profileRow struct {
		fleet.Profile
		SettingsText string
		State        string // "apply" | "current" | "reapply" | "conflict"
	}
	prof := make([]profileRow, 0, profiles.Len())
	for _, p := range profiles.All() {
		var lines []string
		for _, k := range sortedKeys(p.Settings) {
			lines = append(lines, fmt.Sprintf("%s = %s", k, renderValue(p.Settings[k])))
		}
		state := "apply"
		if pol, ok := f.Policies[p.Name]; ok {
			switch {
			case !strings.HasPrefix(pol.Profile, p.Name+"@"):
				state = "conflict"
			case pol.Profile == p.Provenance():
				state = "current"
			default:
				state = "reapply"
			}
		}
		prof = append(prof, profileRow{Profile: p,
			SettingsText: strings.Join(lines, "\n"), State: state})
	}
	type frow struct {
		ID, Match string
		Rules     []fleet.FilterRule
	}
	frows := make([]frow, 0, len(f.Filters))
	for _, id := range sortedKeys(f.Filters) {
		fl := f.Filters[id]
		m := fl.Match
		if m == "" {
			m = "all"
		}
		frows = append(frows, frow{ID: id, Match: m, Rules: fl.Rules})
	}
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	s.render(w, "policies", map[string]any{
		"Title": "Policies", "Nav": "policies", "Policies": rows, "Filters": frows,
		"Profiles": prof,
		"Groups":   groups, "PolicyIDs": sortedKeys(f.Policies), "FilterIDs": sortedKeys(f.Filters),
		"RuleRows": []int{0, 1, 2},
		// The filter editor's suggestion lists: the closed attribute
		// vocabulary and the fleet's actual values, so rules are picked from
		// what exists instead of typed from memory.
		"FilterAttrs":  []string{fleet.AttrTag, fleet.AttrClass, fleet.AttrHardware, fleet.AttrAssignedUser, fleet.AttrGroup},
		"FilterValues": filterValueSuggestions(f),
		"CanOwn":       v.roleAt("org").Meets(identity.Owner)}, v)
}

// filterValueSuggestions collects the fleet's existing attribute values
// (tags, classes, hardware profiles, assigned users, groups) as one sorted
// suggestion list for the filter editor's value field.
func filterValueSuggestions(f *fleet.Fleet) []string {
	set := map[string]bool{}
	for tag, d := range f.Devices {
		set[tag] = true
		if d.Class != "" {
			set[d.Class] = true
		}
		if d.Hardware != "" {
			set[d.Hardware] = true
		}
		if d.AssignedUser != "" {
			set[d.AssignedUser] = true
		}
	}
	for g := range f.Groups {
		set[g] = true
	}
	out := make([]string, 0, len(set))
	for v := range set {
		out = append(out, v)
	}
	sort.Strings(out)
	return out
}
