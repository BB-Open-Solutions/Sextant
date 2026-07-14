package web

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// --- pages ---

func (s *Server) overview(w http.ResponseWriter, r *http.Request, v view) {
	// Every page renders the visible slice of the document, never the
	// whole fleet: per-scope read-confidentiality.
	f := s.svc.Config.Fleet().VisibleTo(v.canView)

	// Scope selector: the same dashboard, computed for org (default), one
	// group (including its subtree), or one device. ?scope= is validated
	// against the visible fleet and re-checked with the same per-scope read
	// gate every other page uses, so a tampered value answers like a 404
	// rather than leaking an invisible scope's data.
	scope := r.URL.Query().Get("scope")
	if scope == "" {
		scope = "org"
	}
	switch {
	case scope == "org":
		// always visible
	case strings.HasPrefix(scope, "group:"):
		if _, ok := f.Groups[strings.TrimPrefix(scope, "group:")]; !ok || !v.canView(scope) {
			http.NotFound(w, r)
			return
		}
	case strings.HasPrefix(scope, "device:"):
		if _, ok := f.Devices[strings.TrimPrefix(scope, "device:")]; !ok || !v.canView(scope) {
			http.NotFound(w, r)
			return
		}
	default:
		http.NotFound(w, r)
		return
	}
	inScope := scopeFilter(f, scope)

	// Scope selector's own drill-down state: which group is selected (own
	// scope, or the group the selected device belongs to), and the device
	// list narrowed to it - mirrors settingsPage's cascade exactly.
	selGroup := ""
	if g, ok := strings.CutPrefix(scope, "group:"); ok {
		selGroup = g
	} else if tag, ok := strings.CutPrefix(scope, "device:"); ok {
		if d, ok := f.Devices[tag]; ok && len(d.Groups) > 0 {
			selGroup = d.Groups[0]
		}
	}
	groupNames := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groupNames = append(groupNames, g)
	}
	sort.Strings(groupNames)
	deviceTags := make([]string, 0, len(f.Devices))
	for tag, d := range f.Devices {
		if selGroup != "" && !deviceInGroup(d, selGroup) {
			continue
		}
		deviceTags = append(deviceTags, tag)
	}
	sort.Strings(deviceTags)

	// Devices in scope, filtered from what is already loaded (no extra store
	// calls): drives the device-count stat, the capacity donut and the
	// compliance total.
	scopedDevices := make(map[string]fleet.Device, len(f.Devices))
	for tag, d := range f.Devices {
		if inScope(tag) {
			scopedDevices[tag] = d
		}
	}

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
	// Compliance is over the ACTIVE fleet: a retired device has no agent, so it
	// is neither healthy nor an incident - counting it would drag the score.
	// Scoped to the selected group/device via scopedDevices.
	total := 0
	for _, d := range scopedDevices {
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
			"Devices": len(scopedDevices), "Online": online, "Groups": len(f.Groups),
			"Policies": len(f.Policies), "OpenChanges": openChanges,
		},
		"Compliance":  map[string]int{"Healthy": healthy, "Warning": warn, "Critical": crit, "Total": total, "Score": hp},
		"Donut":       donut,
		"Capacity":    fleetCapacity(&fleet.Fleet{Devices: scopedDevices}),
		"Utilization": fleetUtilization(status),
		"Incidents":   incidents,
		"Attention":   attn,
		"Approvals":   approvals,
		"Status":      status,
		"CanEnroll":   v.roleAt("org").Meets(identity.Editor),
		// Scope selector state (mirrors settingsPage's cascade: org -> group,
		// including subtree -> device).
		"Scope":    scope,
		"SelGroup": selGroup,
		"IsDevice": strings.HasPrefix(scope, "device:"),
		"Groups":   groupNames,
		"Devices":  deviceTags,
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
		out = append(out, incidentRow{Severity: sev, Title: in.Title, Detail: in.Detail,
			Action: in.Action, Tag: in.Tag, Link: "/devices/" + in.Tag})
		if len(out) >= 8 {
			break
		}
	}
	return out
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	statuses := map[string]app.StatusView{}
	if s.svc.Inventory != nil {
		all, _ := s.svc.Inventory.StatusAll(r.Context())
		for _, st := range all {
			statuses[st.Tag] = st
		}
	}
	type row struct {
		Tag, Class, Hardware, AssignedUser, Revision string
		Groups                                       []string
		HasStatus, Online                            bool
		// Reported is set when the device sent a live usage reading; CPU/RAM/Disk
		// are its used-percentages for the compact per-device resource column.
		Reported       bool
		CPU, RAM, Disk int
	}
	rows := make([]row, 0, len(f.Devices))
	for _, tag := range f.DeviceTags() {
		d := f.Devices[tag]
		st, has := statuses[tag]
		rw := row{Tag: tag, Class: d.Class, Hardware: d.Hardware,
			AssignedUser: d.AssignedUser, Groups: d.Groups,
			HasStatus: has, Online: st.Online, Revision: st.Revision}
		if has && st.Usage.Reported() {
			rw.Reported = true
			rw.CPU = st.Usage.CPUPct
			rw.RAM = pctOf(st.Usage.MemUsedMB, st.Usage.MemTotalMB)
			rw.Disk = pctOf(st.Usage.DiskUsedGB, st.Usage.DiskTotalGB)
		}
		rows = append(rows, rw)
	}
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	s.render(w, "devices", map[string]any{"Title": "Devices", "Nav": "devices",
		"Devices": rows, "Groups": groups,
		"CanEdit": v.roleAt("org").Meets(identity.Editor)}, v)
}

// postDeviceEnroll enrolls a device from the console form.
func (s *Server) postDeviceEnroll(w http.ResponseWriter, r *http.Request, v view) error {
	tag := strings.TrimSpace(r.FormValue("tag"))
	group := r.FormValue("group")
	scope := "org"
	var groups []string
	if group != "" {
		scope = "group:" + group
		groups = []string{group}
	}
	if err := s.requireWeb(v, scope, identity.Editor); err != nil {
		return err
	}
	d := fleet.Device{
		Hardware: strings.TrimSpace(r.FormValue("hardware")),
		Class:    strings.TrimSpace(r.FormValue("class")),
		Groups:   groups,
	}
	msg := fmt.Sprintf("devices: enroll %s (%s)", tag, d.Hardware)
	if err := s.svc.Config.Apply(r.Context(), fleet.AddDevice(tag, d), msg, webAuthor(v), tag); err != nil {
		return err
	}
	// Issue the per-device credential (ADR 0008) and show it once on the
	// device page; enrollment still succeeds if issuing fails (re-issue).
	if s.svc.DevCreds != nil {
		if secret, err := s.svc.DevCreds.Issue(r.Context(), tag); err != nil {
			s.log.Error("device enrolled but credential not issued", "tag", tag, "err", err)
		} else {
			setDevCredCookie(w, tag, secret)
		}
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

func (s *Server) device(w http.ResponseWriter, r *http.Request, v view) {
	tag := r.PathValue("tag")
	f := s.svc.Config.Fleet()
	d, ok := f.Devices[tag]
	// An invisible device answers exactly like a missing one.
	if !ok || !v.canView("device:"+tag) {
		http.NotFound(w, r)
		return
	}
	data := map[string]any{
		"Title": "Device " + tag, "Nav": "devices",
		"Tag": tag, "Device": d, "Retired": d.Retired(),
		"Intent":   d.Intent,
		"Resolved": f.ResolveSorted(tag),
		"CanEdit":  v.roleAt("device:" + tag).Meets(identity.Editor),
		"CanOwn":   v.roleAt("org").Meets(identity.Owner),
	}
	type groupOpt struct {
		Name   string
		Member bool
	}
	// Options: groups the user may view, plus the device's current
	// memberships (which must stay listed, or saving the form would
	// silently drop an invisible membership). Other groups stay hidden -
	// read-confidentiality covers names too.
	groups := make([]groupOpt, 0, len(f.Groups))
	for g := range f.Groups {
		member := slices.Contains(d.Groups, g)
		if !member && !v.canView("group:"+g) {
			continue
		}
		groups = append(groups, groupOpt{Name: g, Member: member})
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].Name < groups[j].Name })
	data["GroupOpts"] = groups
	pkgs, flats, ovs := f.ResolveApps(tag)
	data["Packages"], data["Flatpaks"], data["Overlays"] = pkgs, flats, ovs
	if s.svc.Inventory != nil {
		if st, has, _ := s.svc.Inventory.Status(r.Context(), tag); has {
			data["HasStatus"], data["Status"] = true, st
			data["Posture"] = s.postureView(f, tag, st)
			// Live usage gauges: reuse the fleet aggregation for this one device.
			if st.Usage.Reported() {
				data["Util"] = fleetUtilization([]app.StatusView{st})
			}
		}
		if facts, at, has, _ := s.svc.Inventory.Facts(r.Context(), tag); has {
			data["Facts"], data["FactsAt"] = string(facts), at
		}
	}
	// Attention: the incidents raised for this device (scoped to the viewer).
	var devInc []incidentRow
	for _, in := range s.scopedIncidents(r, v, nil) {
		if in.Tag == tag {
			devInc = append(devInc, in)
		}
	}
	data["Incidents"] = devInc
	// Recent activity: the configuration changes that touched this device,
	// newest first (git commits whose subject names the tag).
	if entries, err := s.svc.Config.AuditLog(r.Context(), 200); err == nil {
		acts := make([]ports.AuditEntry, 0, 8)
		for _, e := range entries {
			if strings.Contains(e.Subject, tag) {
				acts = append(acts, e)
				if len(acts) >= 8 {
					break
				}
			}
		}
		data["Activity"] = acts
	}
	// One-shot device credential from enroll/re-issue/reactivate.
	if c, err := r.Cookie(devCredCookie); err == nil && c.Value != "" {
		data["Credential"] = c.Value
		http.SetCookie(w, &http.Cookie{Name: devCredCookie, Value: "",
			Path: "/devices/" + tag, MaxAge: -1, HttpOnly: true, Secure: true})
	}
	s.render(w, "device", data, v)
}

func (s *Server) policies(w http.ResponseWriter, _ *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	type prow struct {
		ID, Description string
		Settings        map[string]any
		SettingsText    string // editable key = value form
		Enforced        []string
		EnforcedText    string
		Assignments     []fleet.Assignment
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
		rows = append(rows, prow{ID: id, Description: p.Description,
			Settings: p.Settings, SettingsText: strings.Join(lines, "\n"),
			Enforced: p.Enforced, EnforcedText: strings.Join(p.Enforced, ", "),
			Assignments: asn})
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
		"Groups": groups, "PolicyIDs": sortedKeys(f.Policies), "FilterIDs": sortedKeys(f.Filters),
		"RuleRows": []int{0, 1, 2},
		"CanOwn":   v.roleAt("org").Meets(identity.Owner)}, v)
}

func (s *Server) changesPage(w http.ResponseWriter, r *http.Request, v view) {
	// Diffs expose every scope: org-wide read required.
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	crs, err := s.svc.Changes.List(r.Context())
	f := s.svc.Config.Fleet()
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	data := map[string]any{"Title": "Changes", "Nav": "changes", "Changes": crs,
		"Groups":  groups,
		"CanEdit": v.roleAt("org").Meets(identity.Editor)}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "changes", data, v)
}

// diffPage shows an approver what a change would apply.
func (s *Server) diffPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	cr, ok, err := s.svc.Changes.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	diff, err := s.svc.Changes.Diff(r.Context(), id)
	data := map[string]any{"Title": "Diff " + id, "Nav": "changes",
		"ID": id, "Change": cr, "Diff": diff}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "diff", data, v)
}

func webAuthor(v view) ports.Author {
	email := v.User.Email
	if email == "" {
		email = v.User.Subject + "@idp"
	}
	return ports.Author{Subject: v.User.Subject, Name: v.User.Name, Email: email}
}

// parseValue interprets a form value: booleans and integers become typed,
// everything else stays a string.
func parseValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && fmt.Sprint(n) == s {
		return n
	}
	return s
}

func (s *Server) postDeviceSetting(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	ref := "device:" + tag
	if err := s.requireWeb(v, ref, identity.Editor); err != nil {
		return err
	}
	key := strings.TrimSpace(r.FormValue("key"))
	val := parseValue(strings.TrimSpace(r.FormValue("value")))
	if key == "" {
		return fmt.Errorf("setting key required")
	}
	msg := fmt.Sprintf("settings: set %s at %s", key, ref)
	if err := s.svc.Config.Apply(r.Context(), fleet.SetScopeSetting(ref, key, val),
		msg, webAuthor(v), app.AffectedHosts(s.svc.Config.Fleet(), ref)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}

func (s *Server) postChange(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	_, err := s.svc.Changes.Open(r.Context(), r.FormValue("id"), r.FormValue("title"), webAuthor(v))
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/changes", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeSubmit(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Submit(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	http.Redirect(w, r, "/changes", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeMerge(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Merge(r.Context(), r.PathValue("id"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/changes", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeAbandon(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Abandon(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	http.Redirect(w, r, "/changes", http.StatusSeeOther)
	return nil
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
