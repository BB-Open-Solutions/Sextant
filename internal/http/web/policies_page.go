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
	// arow is one assignment with its live reach, so a filter that excludes
	// the whole target reads "0 devices" instead of silently applying to
	// nothing.
	type arow struct {
		fleet.Assignment
		Devices int
	}
	type prow struct {
		ID, Description string
		Settings        map[string]any
		SettingsText    string // editable key = value form
		Enforced        []string
		EnforcedText    string
		Assignments     []arow
		Profile         string // source profile name, "" for hand-made
		Drift           bool   // the overlay's profile moved past this stamp
		Edited          bool   // settings hand-edited since the apply (stamp
		// only moves with the overlay, so local divergence is content-checked)
	}
	rows := make([]prow, 0, len(f.Policies))
	for _, id := range sortedKeys(f.Policies) {
		p := f.Policies[id]
		var asn []arow
		for _, a := range f.Assignments {
			if a.Policy == id {
				asn = append(asn, arow{Assignment: a, Devices: len(f.AssignmentDevices(a))})
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
				st := profileState(p, src)
				row.Drift = st == "reapply"
				row.Edited = st == "edited"
			}
		}
		rows = append(rows, row)
	}
	// The overlay's recommended profiles, each with its instantiation state:
	// apply (never instantiated), reapply (drifted behind the overlay),
	// edited (settings hand-changed since the apply - the stamp cannot see
	// this, only content comparison can), current (matches), or conflict
	// (a hand-made policy owns the id).
	type profileRow struct {
		fleet.Profile
		SettingsText string
		State        string // "apply" | "current" | "reapply" | "edited" | "conflict"
	}
	prof := make([]profileRow, 0, profiles.Len())
	for _, p := range profiles.All() {
		var lines []string
		for _, k := range sortedKeys(p.Settings) {
			lines = append(lines, fmt.Sprintf("%s = %s", k, renderValue(p.Settings[k])))
		}
		state := "apply"
		if pol, ok := f.Policies[p.Name]; ok {
			state = profileState(pol, p)
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

// profileState classifies a policy against its source profile: "" (hand-made
// or no matching profile), "current", "reapply" (the overlay's profile moved
// past the stamp), "edited" (settings hand-changed since the apply - the
// stamp cannot see this, only content comparison can), or "conflict" (a
// hand-made policy occupies the profile's id). The single source of truth
// for drift, consumed by the policy rows and the profile cards alike.
func profileState(pol fleet.Policy, src fleet.Profile) string {
	if !strings.HasPrefix(pol.Profile, src.Name+"@") {
		return "conflict"
	}
	switch {
	case pol.Profile != src.Provenance():
		return "reapply"
	case !src.SettingsMatch(pol.Settings):
		return "edited"
	default:
		return "current"
	}
}
