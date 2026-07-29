package web

// compliance.go: the deviations overview - one page that answers "which
// devices are not to spec, and why". The overview's donut summarises the
// same incident signal; this is where its "view details" lands.

import (
	"net/http"
	"slices"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
)

// complianceRow is one device's compliance verdict.
type complianceRow struct {
	Tag          string
	Groups       []string
	Class        string
	Hardware     string
	AssignedUser string
	// Status: critical | warning | ok. Rank orders the table (worst first).
	Status string
	Rank   int
	// Issues are the device's open incidents (title + suggested action).
	Issues []incidentIssue
}

type incidentIssue struct{ Title, Detail, Action string }

// compliancePage lists every visible active device with its worst status and
// the incidents behind it, worst first, plus a per-policy exposure summary.
func (s *Server) compliancePage(w http.ResponseWriter, r *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)

	// All visible incidents, grouped per device (the overview caps at 8 for
	// its attention queue; this page is the full account).
	byTag := map[string][]incident.Incident{}
	// Fleet-level incidents (a stalled rollout) name no device, so they have
	// no row in the table below and would vanish here. They get their own
	// band above it - dropping them is exactly the silence they exist to end.
	var fleetWide []incidentIssue
	if s.svc.Compliance != nil {
		if all, err := s.svc.Compliance.Incidents(r.Context()); err != nil {
			s.log.Warn("compliance incidents failed", "err", err)
		} else {
			for _, in := range all {
				if !v.canView(in.Scope) {
					continue
				}
				if in.Tag == "" {
					fleetWide = append(fleetWide, incidentIssue{Title: in.Title, Detail: in.Detail, Action: in.Action})
					continue
				}
				byTag[in.Tag] = append(byTag[in.Tag], in)
			}
		}
	}

	classSet := map[string]bool{}
	rows := make([]complianceRow, 0, len(f.Devices))
	counts := map[string]int{}
	for _, tag := range f.DeviceTags() {
		d := f.Devices[tag]
		if d.Retired() {
			continue
		}
		row := complianceRow{Tag: tag, Groups: d.Groups, Class: d.Class,
			Hardware: d.Hardware, AssignedUser: d.AssignedUser, Status: "ok", Rank: 2}
		for _, in := range byTag[tag] {
			row.Issues = append(row.Issues, incidentIssue{Title: in.Title, Detail: in.Detail, Action: in.Action})
			if in.Severity == incident.Critical {
				row.Status, row.Rank = "critical", 0
			} else if row.Status != "critical" {
				row.Status, row.Rank = "warning", 1
			}
		}
		if d.Class != "" {
			classSet[d.Class] = true
		}
		counts[row.Status]++
		rows = append(rows, row)
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Rank != rows[j].Rank {
			return rows[i].Rank < rows[j].Rank
		}
		return rows[i].Tag < rows[j].Tag
	})

	// The same filter bar as the device fleet: search + class + group, plus
	// the status facet (here the severity verdict). Counts above stay
	// fleet-wide so the summary keeps meaning under any filter.
	qy := r.URL.Query()
	q := strings.ToLower(strings.TrimSpace(qy.Get("q")))
	fClass, fGroup, fStatus := qy.Get("class"), qy.Get("group"), qy.Get("status")
	kept := rows[:0]
	for _, row := range rows {
		if q != "" && !strings.Contains(strings.ToLower(row.Tag), q) &&
			!strings.Contains(strings.ToLower(row.AssignedUser), q) &&
			!strings.Contains(strings.ToLower(row.Hardware), q) {
			continue
		}
		if fClass != "" && row.Class != fClass {
			continue
		}
		if fGroup != "" && !slices.Contains(row.Groups, fGroup) {
			continue
		}
		if fStatus != "" && row.Status != fStatus {
			continue
		}
		kept = append(kept, row)
	}
	rows = kept

	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	classes := make([]string, 0, len(classSet))
	for c := range classSet {
		classes = append(classes, c)
	}
	sort.Strings(classes)

	// Every risk acceptance in one place (comply-or-explain, ADR 0007):
	// accepting a control is a compliance decision, so it is managed HERE,
	// not scattered over the settings editor (Bram, 17 jul).
	type acceptanceRow struct{ Scope, Key, Reason string }
	var acceptances []acceptanceRow
	scopes := make([]string, 1, 1+len(f.Groups)+len(f.Devices))
	scopes[0] = "org"
	for g := range f.Groups {
		scopes = append(scopes, "group:"+g)
	}
	for d := range f.Devices {
		scopes = append(scopes, "device:"+d)
	}
	sort.Strings(scopes[1:])
	for _, sc := range scopes {
		if acc, _ := f.AcceptancesAt(sc); acc != nil {
			keys := make([]string, 0, len(acc))
			for k := range acc {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			for _, k := range keys {
				acceptances = append(acceptances, acceptanceRow{Scope: sc, Key: k, Reason: acc[k]})
			}
		}
	}

	s.render(w, "compliance", map[string]any{
		"Title": "Compliance", "Nav": "compliance",
		"Rows":      rows,
		"FleetWide": fleetWide,
		"Critical":  counts["critical"], "Warning": counts["warning"], "OK": counts["ok"],
		"Total": counts["critical"] + counts["warning"] + counts["ok"],
		"Q":     qy.Get("q"), "FClass": fClass, "FGroup": fGroup, "FStatus": fStatus,
		"Groups": groups, "Classes": classes,
		"Acceptances":  acceptances,
		"AcceptScopes": scopes,
		"CanAccept":    v.roleAt("org").Meets(identity.Owner),
		"Policies":     policyExposure(f, byTag),
	}, v)
}

// policyRow is one policy's exposure: where it is assigned and how many of
// the devices under those targets currently carry an open incident - the
// revision-level proxy for "this policy may not be applied there yet".
type policyRow struct {
	ID         string
	Targets    []string
	Devices    int
	WithIssues int
}

func policyExposure(f *fleet.Fleet, byTag map[string][]incident.Incident) []policyRow {
	out := make([]policyRow, 0, len(f.Policies))
	for _, id := range sortedKeys(f.Policies) {
		row := policyRow{ID: id}
		seen := map[string]bool{}
		for _, a := range f.Assignments {
			if a.Policy != id {
				continue
			}
			row.Targets = append(row.Targets, a.Target)
			for _, tag := range f.TargetDevices(a.Target) {
				if seen[tag] {
					continue
				}
				seen[tag] = true
				row.Devices++
				if len(byTag[tag]) > 0 {
					row.WithIssues++
				}
			}
		}
		out = append(out, row)
	}
	return out
}
