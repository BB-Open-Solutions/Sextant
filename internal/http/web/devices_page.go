package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// deviceRows builds the full (unfiltered) device table for a fleet view:
// status join, usage columns and the design-0008 baseline verdict. Shared by
// the devices page and its CSV export so both always show the same truth.
func (s *Server) deviceRows(ctx context.Context, f *fleet.Fleet) []deviceRow {
	statuses := map[string]app.StatusView{}
	if s.svc.Inventory != nil {
		all, _ := s.svc.Inventory.StatusAll(ctx)
		for _, st := range all {
			statuses[st.Tag] = st
		}
	}
	judge := app.NewBaselineJudge(f, s.svc.Config.Profiles())
	rows := make([]deviceRow, 0, len(f.Devices))
	for _, tag := range f.DeviceTags() {
		d := f.Devices[tag]
		st, has := statuses[tag]
		rw := deviceRow{Tag: tag, Class: d.Class, Hardware: d.Hardware,
			AssignedUser: d.AssignedUser, Groups: d.Groups,
			HasStatus: has, Online: st.Online, Revision: st.Revision}
		if has && st.Usage.Reported() {
			rw.Reported = true
			rw.CPU = st.Usage.CPUPct
			rw.RAM = pctOf(st.Usage.MemUsedMB, st.Usage.MemTotalMB)
			rw.Disk = pctOf(st.Usage.DiskUsedGB, st.Usage.DiskTotalGB)
		}
		if d.State != fleet.DeviceRetired {
			b := judge.Verdict(tag, st, has)
			if b.Compliant {
				rw.Baseline = "ok"
			} else {
				rw.Baseline = "attention"
			}
			rw.BaselineFails = b.Failures
		}
		rows = append(rows, rw)
	}
	return rows
}

func (s *Server) devices(w http.ResponseWriter, r *http.Request, v view) {
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	rows := s.deviceRows(r.Context(), f)
	classSet := map[string]bool{}
	for _, rw := range rows {
		if rw.Class != "" {
			classSet[rw.Class] = true
		}
	}

	// Search, filter and sort happen server-side so they hold at fleet scale
	// (the list is never shipped whole to the client to sort). Every control is
	// a GET param, so the view is shareable/bookmarkable and needs no JS.
	qy := r.URL.Query()
	q := strings.ToLower(strings.TrimSpace(qy.Get("q")))
	fClass, fGroup, fStatus := qy.Get("class"), qy.Get("group"), qy.Get("status")
	fBaseline := qy.Get("baseline")
	rows = filterDeviceRows(rows, q, fClass, fGroup, fStatus, fBaseline)
	sortKey, dir := qy.Get("sort"), qy.Get("dir")
	sortDeviceRows(rows, sortKey, dir)

	// Paginate AFTER filter+sort so the page shows a stable slice of the
	// whole result: at 10,000 devices a single response must never carry
	// every row (fleet-scale posture, docs/architecture/scale.md).
	const perPage = 100
	total := len(rows)
	pages := (total + perPage - 1) / perPage
	if pages == 0 {
		pages = 1
	}
	page, _ := strconv.Atoi(qy.Get("page"))
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	lo := (page - 1) * perPage
	hi := min(lo+perPage, total)
	rows = rows[lo:hi]

	// Release numbers for the visible page only, deduped per revision: cheap
	// via ConfigService's relCache, but no reason to look up more than what
	// renders. Unknown (0) leaves the row to fall back to the short hash.
	relSeen := map[string]int{}
	for i := range rows {
		if rows[i].Revision == "" {
			continue
		}
		rel, ok := relSeen[rows[i].Revision]
		if !ok {
			rel = s.svc.Config.ReleaseNumber(r.Context(), rows[i].Revision)
			relSeen[rows[i].Revision] = rel
		}
		rows[i].Release = rel
	}

	// The pager links re-carry every filter/sort control.
	baseQ := url.Values{}
	for _, k := range []string{"q", "class", "group", "status", "baseline", "sort", "dir"} {
		if val := qy.Get(k); val != "" {
			baseQ.Set(k, val)
		}
	}
	pageURL := func(p int) string {
		u := url.Values{}
		for k, vs := range baseQ {
			u[k] = vs
		}
		u.Set("page", strconv.Itoa(p))
		return "/devices?" + u.Encode()
	}

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
	data := map[string]any{"Title": "Devices", "Nav": "devices",
		"Devices": rows, "Groups": groups, "Classes": classes,
		"Q": qy.Get("q"), "FClass": fClass, "FGroup": fGroup, "FStatus": fStatus,
		"FBaseline": fBaseline,
		"Sort":      sortKey, "Dir": dir,
		"Total": total, "Page": page, "Pages": pages,
		"From": lo + 1, "To": hi,
		"CanEdit":   v.roleAt("org").Meets(identity.Editor),
		"CanExport": v.roleAt("org").Meets(identity.Viewer),
		// The export honours the active filters: a filtered view exports
		// exactly what it shows (design 0008).
		"ExportURL": "/devices.csv?" + baseQ.Encode()}
	if page > 1 {
		data["PrevURL"] = pageURL(page - 1)
	}
	if page < pages {
		data["NextURL"] = pageURL(page + 1)
	}
	s.render(w, "devices", data, v)
}

// deviceRow is one row of the device fleet table.
type deviceRow struct {
	Tag, Class, Hardware, AssignedUser, Revision string
	// Release is the human release number for Revision (0 = unknown; the
	// template falls back to the short hash), set after pagination.
	Release           int
	Groups            []string
	HasStatus, Online bool
	// Reported is set when the device sent a live usage reading; CPU/RAM/Disk
	// are its used-percentages for the compact per-device resource column.
	Reported       bool
	CPU, RAM, Disk int
	// Baseline is the design-0008 verdict: "ok", "attention" or "" for a
	// retired device (nothing to judge). BaselineFails names the failing
	// criteria for the hover title and the CSV.
	Baseline      string
	BaselineFails []string
}

// filterDeviceRows keeps rows matching a case-insensitive search over
// tag/user/hardware plus the class/group/status facets. Empty facets match all.
// It filters in place (reusing the backing array), so the caller reassigns.
func filterDeviceRows(rows []deviceRow, q, class, group, status, baseline string) []deviceRow {
	out := rows[:0]
	for _, rw := range rows {
		if q != "" && !strings.Contains(strings.ToLower(rw.Tag), q) &&
			!strings.Contains(strings.ToLower(rw.AssignedUser), q) &&
			!strings.Contains(strings.ToLower(rw.Hardware), q) {
			continue
		}
		if class != "" && rw.Class != class {
			continue
		}
		if group != "" && !slices.Contains(rw.Groups, group) {
			continue
		}
		if baseline != "" && rw.Baseline != baseline {
			continue
		}
		switch status {
		case "online":
			if !rw.Online {
				continue
			}
		case "offline":
			if !rw.HasStatus || rw.Online {
				continue
			}
		case "never":
			if rw.HasStatus {
				continue
			}
		}
		out = append(out, rw)
	}
	return out
}

// sortDeviceRows orders rows by one column, ascending unless dir=="desc"; the
// default and fallback is by tag.
func sortDeviceRows(rows []deviceRow, key, dir string) {
	less := func(a, b deviceRow) bool { return a.Tag < b.Tag }
	switch key {
	case "status":
		less = func(a, b deviceRow) bool { return deviceStatusRank(a) < deviceStatusRank(b) }
	case "hardware":
		less = func(a, b deviceRow) bool { return a.Hardware < b.Hardware }
	case "class":
		less = func(a, b deviceRow) bool { return a.Class < b.Class }
	case "user":
		less = func(a, b deviceRow) bool { return a.AssignedUser < b.AssignedUser }
	case "baseline":
		less = func(a, b deviceRow) bool { return baselineRank(a) < baselineRank(b) }
	}
	sort.SliceStable(rows, func(i, j int) bool {
		if dir == "desc" {
			return less(rows[j], rows[i])
		}
		return less(rows[i], rows[j])
	})
}

// baselineRank orders needs-attention before compliant before not-judged
// (retired), so the ascending sort surfaces the action items first.
func baselineRank(r deviceRow) int {
	switch r.Baseline {
	case "attention":
		return 0
	case "ok":
		return 1
	default:
		return 2
	}
}

// deviceStatusRank orders online before offline before never-seen.
func deviceStatusRank(r deviceRow) int {
	switch {
	case r.Online:
		return 0
	case r.HasStatus:
		return 1
	default:
		return 2
	}
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
	// Class options: the controlled vocabulary, so the identity card's class
	// field is picked from one source of truth, not retyped from memory. A
	// device carrying a legacy/unknown value is preserved as an extra option
	// by the template so its record still renders.
	data["AllClasses"] = fleet.Classes
	pkgs, flats, ovs := f.ResolveApps(tag)
	data["Packages"], data["Flatpaks"], data["Overlays"] = pkgs, flats, ovs
	var st app.StatusView
	var hasSt bool
	if s.svc.Inventory != nil {
		st, hasSt, _ = s.svc.Inventory.Status(r.Context(), tag)
		if hasSt {
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
	// Baseline verdict (design 0008) with its failing criteria spelled out.
	if !d.Retired() {
		data["Baseline"] = app.NewBaselineJudge(f, s.svc.Config.Profiles()).Verdict(tag, st, hasSt)
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
	// Device secrets (LUKS recovery, break-glass admin): owner-only, one-shot
	// reveal with an audit stamp. Only the metadata reaches the page - which
	// kinds exist and their reveal history - never any plaintext.
	if v.roleAt("org").Meets(identity.Owner) {
		if s.svc.DeviceSecrets != nil && s.svc.DeviceSecrets.Enabled() {
			data["SecretsEnabled"] = true
			if metas, err := s.svc.DeviceSecrets.List(r.Context(), tag); err != nil {
				s.log.Warn("device secrets list", "tag", tag, "err", err)
			} else {
				data["Secrets"] = metas
			}
		} else {
			data["SecretsEnabled"] = false
		}
	}
	// One-shot device credential from enroll/re-issue/reactivate.
	if c, err := r.Cookie(devCredCookie); err == nil && c.Value != "" {
		data["Credential"] = c.Value
		// #nosec G124 - deletion of the one-shot credential cookie: empty value, MaxAge -1, HttpOnly+Secure set; nothing to protect.
		http.SetCookie(w, &http.Cookie{Name: devCredCookie, Value: "",
			Path: "/devices/" + tag, MaxAge: -1, HttpOnly: true, Secure: true})
	}
	s.render(w, "device", data, v)
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
	if err := s.applyGated(r, v, fleet.SetScopeSetting(ref, key, val),
		msg, app.AffectedHosts(s.svc.Config.Fleet(), ref)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
