package web

import (
	"net/http"
	"slices"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
)

// --- pages ---

func (s *Server) overview(w http.ResponseWriter, r *http.Request, v view) {
	// Every page renders the visible slice of the document, never the
	// whole fleet: per-scope read-confidentiality.
	f := s.svc.Config.Fleet().VisibleTo(v.canView)

	// The dashboard is fleet-wide (no scope selector): the visible slice above
	// already enforces per-scope read-confidentiality, and the "org" filter
	// passes every device the viewer may see.
	inScope := scopeFilter(f, "org")

	var status []app.StatusView
	if s.svc.Inventory != nil {
		all, _ := s.svc.Inventory.StatusAll(r.Context())
		for _, st := range all {
			if v.canView("device:"+st.Tag) && inScope(st.Tag) {
				status = append(status, st)
			}
		}
	}
	online := 0
	type attention struct{ Kind, Detail string }
	var attn []attention
	for _, st := range status {
		if st.Online {
			online++
		}
		if st.Error != "" {
			attn = append(attn, attention{"device error", st.Tag + ": " + st.Error})
		}
	}
	openChanges := 0
	// Approvals awaiting this user: a change built green (Ready) that they may
	// merge. Under four-eyes a user cannot approve their own change.
	type approval struct{ ID, Author string }
	var approvals []approval
	fourEyes := f.Assurance != nil && f.Assurance.RequireFourEyes
	canApprove := v.roleAt("org").Meets(identity.Editor)
	// Change requests span the whole document; only org-wide viewers see them.
	if s.svc.Changes != nil && v.canView("org") {
		crs, _ := s.svc.Changes.List(r.Context())
		for _, cr := range crs {
			if cr.Open() {
				openChanges++
			}
			if cr.Status == "failed" {
				attn = append(attn, attention{"change failed", cr.ID + ": " + cr.Error})
			}
			if cr.Status == change.Ready && canApprove && (!fourEyes || cr.AuthorSubject != v.User.Subject) {
				approvals = append(approvals, approval{ID: cr.ID, Author: cr.Author})
			}
		}
	}
	// Compliance drives the attention queue and the health donut: incidents the
	// viewer may see (scoped to their groups) and further narrowed to the
	// selected scope, plus a per-device worst-severity tally for the donut
	// (healthy / warning / critical).
	incidents := s.scopedIncidents(r, v, inScope)
	crit, warn := 0, 0
	worst := map[string]int{} // device tag -> worst severity seen (2 crit, 1 warn)
	for _, in := range incidents {
		if in.Tag == "" {
			continue // fleet-level (a stalled run): no device to colour
		}
		sev := 1
		if in.Severity == "critical" {
			sev = 2
		}
		if sev > worst[in.Tag] {
			worst[in.Tag] = sev
		}
	}
	for _, s := range worst {
		if s == 2 {
			crit++
		} else {
			warn++
		}
	}
	// Compliance is over the ACTIVE, visible fleet: a retired device has no
	// agent, so it is neither healthy nor an incident - counting it would drag
	// the score.
	total := 0
	for _, d := range f.Devices {
		if !d.Retired() {
			total++
		}
	}
	healthy := total - crit - warn
	if healthy < 0 {
		healthy = 0
	}
	pct := func(n int) int {
		if total == 0 {
			return 0
		}
		return n * 100 / total
	}
	hp, wp, cp := pct(healthy), pct(warn), pct(crit)
	// Stacked donut: each ring is dasharray "<pct> 100" offset by the sum of the
	// rings before it (r=15.915 makes the circumference 100, so pct == length).
	donut := []map[string]any{
		{"Color": "#00d4a4", "Dash": hp, "Offset": 0},
		{"Color": "#c37d0d", "Dash": wp, "Offset": -hp},
		{"Color": "#d45656", "Dash": cp, "Offset": -(hp + wp)},
	}

	s.render(w, "overview", map[string]any{
		"Title": "Overview", "Nav": "overview",
		"Stats": map[string]int{
			// Device count and online are scoped; groups/policies/open-changes
			// are fleet-wide vocabulary (open changes are org-only by nature -
			// see the guard above) and stay as-is at every scope.
			"Devices": len(f.Devices), "Online": online, "Groups": len(f.Groups),
			"Policies": len(f.Policies), "OpenChanges": openChanges,
		},
		"Compliance":  map[string]int{"Healthy": healthy, "Warning": warn, "Critical": crit, "Total": total, "Score": hp},
		"Donut":       donut,
		"Utilization": fleetUtilization(status),
		"Incidents":   incidents,
		"Attention":   attn,
		"Approvals":   approvals,
		"Status":      status,
		"CanEnroll":   v.roleAt("org").Meets(identity.Editor),
	}, v)
}

// scopeFilter compiles a scope ref into a device-tag membership test: every
// visible device for "org", a device scope's exact tag, or a group scope's
// whole subtree (a device counts if the scope group is anywhere in the
// ancestry of any group it belongs to) - the same subtree rule
// app.AffectedHosts uses to compute a change's blast radius.
func scopeFilter(f *fleet.Fleet, scope string) func(tag string) bool {
	switch {
	case strings.HasPrefix(scope, "device:"):
		tag := strings.TrimPrefix(scope, "device:")
		return func(t string) bool { return t == tag }
	case strings.HasPrefix(scope, "group:"):
		g := strings.TrimPrefix(scope, "group:")
		members := map[string]bool{}
		for tag, d := range f.Devices {
			for _, dg := range d.Groups {
				if slices.Contains(f.GroupAncestry(dg), g) {
					members[tag] = true
					break
				}
			}
		}
		return func(t string) bool { return members[t] }
	default: // "org"
		return func(string) bool { return true }
	}
}

// incidentRow is one attention-queue item prepared for the overview.
type incidentRow struct {
	Severity string // critical | warning | info
	Title    string
	Detail   string
	Action   string
	Tag      string
	Link     string
}

// scopedIncidents returns the incidents the viewer may see, capped for the
// overview queue. tagOK further narrows to one dashboard scope's devices; nil
// means unrestricted (every visible incident, the device page's own use).
// Empty when compliance (Postgres) is not configured.
func (s *Server) scopedIncidents(r *http.Request, v view, tagOK func(tag string) bool) []incidentRow {
	if s.svc.Compliance == nil {
		return nil
	}
	if tagOK == nil {
		tagOK = func(string) bool { return true }
	}
	all, err := s.svc.Compliance.Incidents(r.Context())
	if err != nil {
		s.log.Warn("overview incidents failed", "err", err)
		return nil
	}
	out := make([]incidentRow, 0, len(all))
	for _, in := range all {
		if !v.canView(in.Scope) || !tagOK(in.Tag) {
			continue
		}
		sev := "warning"
		if in.Severity == incident.Critical {
			sev = "critical"
		}
		// A fleet-level incident (a stalled run) names no device; it links to
		// the rollout monitor, where the wave that is stuck is visible.
		link := "/updates/rollout"
		if in.Tag != "" {
			link = "/devices/" + in.Tag
		}
		out = append(out, incidentRow{Severity: sev, Title: in.Title, Detail: in.Detail,
			Action: in.Action, Tag: in.Tag, Link: link})
		if len(out) >= 8 {
			break
		}
	}
	return out
}
