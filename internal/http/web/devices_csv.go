package web

import (
	"encoding/csv"
	"net/http"
	"strconv"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// devicesCSV streams the devices table - including the design-0008 baseline
// verdict and its failing criteria - as a CSV download: the audit artifact.
// It honours the same filter params as the devices page (a filtered view
// exports what it shows) but never paginates. Org-wide Viewer, like the
// evidence export it sits beside; the export is logged with actor and count
// (reads do not land in the git audit trail, which records config commits).
func (s *Server) devicesCSV(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet().VisibleTo(v.canView)
	rows := s.deviceRows(r.Context(), f)
	qy := r.URL.Query()
	rows = filterDeviceRows(rows, strings.ToLower(strings.TrimSpace(qy.Get("q"))),
		qy.Get("class"), qy.Get("group"), qy.Get("status"), qy.Get("baseline"))
	sortDeviceRows(rows, qy.Get("sort"), qy.Get("dir"))

	name := "devices-baseline-" + time.Now().UTC().Format("2006-01-02") + ".csv"
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	cw := csv.NewWriter(w)
	_ = cw.Write([]string{"tag", "class", "hardware", "assigned_user", "groups",
		"online", "revision", "baseline", "failing_criteria"})
	for _, rw := range rows {
		online := "never-seen"
		if rw.HasStatus {
			online = strconv.FormatBool(rw.Online)
		}
		baseline := rw.Baseline
		if baseline == "" {
			baseline = "retired"
		}
		_ = cw.Write([]string{rw.Tag, rw.Class, rw.Hardware, rw.AssignedUser,
			strings.Join(rw.Groups, " "), online, rw.Revision,
			baseline, strings.Join(rw.BaselineFails, "; ")})
	}
	cw.Flush()
	s.log.Info("baseline csv exported", "by", v.User.Subject, "rows", len(rows))
}
