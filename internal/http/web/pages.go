package web

import (
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// --- pages ---

func (s *Server) overview(w http.ResponseWriter, r *http.Request, v view) {
	// Every page renders the visible slice of the document, never the
	// whole fleet: per-scope read-confidentiality.
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	var status []app.StatusView
	if s.svc.Inventory != nil {
		all, _ := s.svc.Inventory.StatusAll(r.Context())
		for _, st := range all {
			if v.canView("device:" + st.Tag) {
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
		}
	}
	s.render(w, "overview", map[string]any{
		"Title": "Overview", "Nav": "overview",
		"Stats": map[string]int{
			"Devices": len(f.Devices), "Online": online, "Groups": len(f.Groups),
			"Policies": len(f.Policies), "OpenChanges": openChanges,
		},
		"Attention": attn,
		"Status":    status,
	}, v)
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
	}
	rows := make([]row, 0, len(f.Devices))
	for _, tag := range f.DeviceTags() {
		d := f.Devices[tag]
		st, has := statuses[tag]
		rows = append(rows, row{Tag: tag, Class: d.Class, Hardware: d.Hardware,
			AssignedUser: d.AssignedUser, Groups: d.Groups,
			HasStatus: has, Online: st.Online, Revision: st.Revision})
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
		}
		if facts, at, has, _ := s.svc.Inventory.Facts(r.Context(), tag); has {
			data["Facts"], data["FactsAt"] = string(facts), at
		}
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
	var rows []prow
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
	var frows []frow
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

func (s *Server) rolloutPage(w http.ResponseWriter, r *http.Request, v view) {
	// The plan enumerates rings and groups: org-wide read required.
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet()
	data := map[string]any{"Title": "Rollout", "Nav": "rollout",
		"CanOwn":   v.roleAt("org").Meets(identity.Owner),
		"HasRings": f.Rollout != nil && len(f.Rollout.Rings) > 0,
	}
	// Ring-plan editor state: every existing ring plus two blank rows.
	// Sizing to the plan (not a fixed cap) means a large plan can never
	// render truncated and then lose rings on an unrelated save.
	ringRows := 2
	if f.Rollout != nil {
		ringRows += len(f.Rollout.Rings)
	}
	planGroups := make([]string, ringRows)
	planSoaks := make([]string, ringRows)
	planHealthy := make([]string, ringRows)
	if f.Rollout != nil {
		for i, ring := range f.Rollout.Rings {
			planGroups[i] = ring.Group
			if ring.SoakMinutes > 0 {
				planSoaks[i] = fmt.Sprint(ring.SoakMinutes)
			}
			if ring.MinHealthyPercent > 0 {
				planHealthy[i] = fmt.Sprint(ring.MinHealthyPercent)
			}
		}
	}
	rows := make([]int, ringRows)
	for i := range rows {
		rows[i] = i
	}
	allGroups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		allGroups = append(allGroups, g)
	}
	sort.Strings(allGroups)
	data["RingRows"] = rows
	data["AllGroups"] = allGroups
	data["PlanGroups"], data["PlanSoaks"], data["PlanHealthy"] = planGroups, planSoaks, planHealthy
	st, ringStatus, err := s.svc.Rollouts.Status(r.Context())
	if err != nil {
		data["Error"] = err.Error()
	}
	if st != nil {
		data["State"] = st
		type ringRow struct {
			Ring   fleet.RolloutRing
			Status rollout.RingStatus
		}
		var rows []ringRow
		if f.Rollout != nil {
			for i, rr := range f.Rollout.Rings {
				row := ringRow{Ring: rr}
				if i < len(ringStatus) {
					row.Status = ringStatus[i]
				}
				rows = append(rows, row)
			}
		}
		data["Rings"] = rows
	}
	s.render(w, "rollout", data, v)
}

func (s *Server) accessPage(w http.ResponseWriter, r *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	canOwn := v.roleAt("org").Meets(identity.Owner)
	data := map[string]any{
		"Title": "Access", "Nav": "access",
		"Bindings": f.Access, "Groups": groups,
		"CanOwn":   canOwn,
		"FourEyes": f.Assurance != nil && f.Assurance.RequireFourEyes,
	}
	// Directory picker: real IdP groups instead of free text. Best-effort;
	// a slow or absent directory must not break the page.
	if s.svc.Directory != nil && canOwn {
		if dgs, err := s.svc.Directory.ListGroups(r.Context(), ""); err == nil {
			data["DirGroups"] = dgs
		} else {
			s.log.Warn("directory browse failed", "err", err)
		}
	}
	s.render(w, "access", data, v)
}

// --- actions (POST + redirect) ---

func (s *Server) requireWeb(v view, ref string, role identity.Role) error {
	if got := v.roleAt(ref); !got.Meets(role) {
		return fmt.Errorf("requires %s at %s (you hold %s)", role, ref, got)
	}
	return nil
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

func (s *Server) postRolloutStart(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Start(r.Context(), r.FormValue("target"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutTick(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, _, err := s.svc.Rollouts.Tick(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postRolloutCancel(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Rollouts.Cancel(r.Context()); err != nil {
		return err
	}
	http.Redirect(w, r, "/rollout", http.StatusSeeOther)
	return nil
}

func (s *Server) postAccessGrant(w http.ResponseWriter, r *http.Request, v view) error {
	b := fleet.AccessBinding{Group: r.FormValue("group"),
		Role: r.FormValue("role"), Scope: r.FormValue("scope")}
	if err := s.requireWeb(v, b.Scope, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("access: grant %s %s at %s", b.Group, b.Role, b.Scope)
	if err := s.svc.Config.Apply(r.Context(), fleet.Grant(b), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
	return nil
}

func (s *Server) postAccessRevoke(w http.ResponseWriter, r *http.Request, v view) error {
	group, scope := r.FormValue("group"), r.FormValue("scope")
	if err := s.requireWeb(v, scope, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("access: revoke %s at %s", group, scope)
	if err := s.svc.Config.Apply(r.Context(), fleet.Revoke(group, scope), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
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
