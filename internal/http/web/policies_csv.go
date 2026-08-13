package web

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// policiesCSV streams the full policy set with assignments and coverage as a
// CSV download - the audit artifact answering "which rules exist, what do
// they enforce, which framework controls do they implement, and where do
// they land". One row per assignment; an unassigned policy still gets a row
// (an auditor must see dormant rules too). Org-wide Viewer, logged like the
// devices export.
func (s *Server) policiesCSV(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	profiles := s.svc.Config.Profiles()
	behindOf := s.policyBehindCounter(r.Context(), f)

	name := "policies-" + time.Now().UTC().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"policy", "description", "controls", "profile", "state",
		"settings", "enforced_keys", "target", "filter",
		"devices_reached", "devices_behind"})
	rows := 0
	for _, id := range sortedKeys(f.Policies) {
		p := f.Policies[id]
		state := ""
		if pname, _, ok := strings.Cut(p.Profile, "@"); ok {
			if src, has := profiles.Get(pname); has {
				state = profileState(p, src)
			}
		}
		var settings []string
		for _, k := range sortedKeys(p.Settings) {
			settings = append(settings, k+"="+renderValue(p.Settings[k]))
		}
		base := []string{id, p.Description, strings.Join(p.Controls, "; "),
			p.Profile, state, strings.Join(settings, "; "),
			strings.Join(p.Enforced, "; ")}
		wrote := false
		for _, a := range f.Assignments {
			if a.Policy != id {
				continue
			}
			devs := f.AssignmentDevices(a)
			// No priority column (ADR 0026): it decides nothing, and an
			// export that carries it invites a spreadsheet to be built on a
			// number that does not apply.
			_ = cw.Write(append(append([]string{}, base...), a.Target, a.Filter,
				strconv.Itoa(len(devs)), strconv.Itoa(behindOf(devs))))
			rows++
			wrote = true
		}
		if !wrote {
			_ = cw.Write(append(append([]string{}, base...), "", "", "", "0", "0"))
			rows++
		}
	}
	cw.Flush()
	s.log.Info("policies csv exported", "by", v.User.Subject, "rows", rows)
}
