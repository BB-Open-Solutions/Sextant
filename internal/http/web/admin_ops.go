package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// admin_ops.go: organisation administration - audit trail and assurance.
// The access page gains the directory picker in accessPage (pages.go).

// auditPage lists the config commit trail (org Viewer, like diffs).
func (s *Server) auditPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := s.svc.Config.AuditLog(r.Context(), limit)
	data := map[string]any{"Title": "Audit", "Nav": "audit", "Entries": entries}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "audit", data, v)
}

// auditEvidence streams the evidence bundle for a period as a JSON download.
// Session-authed sibling of the API's GET /api/v1/evidence: the console form
// posts plain date inputs (yyyy-mm-dd), so parse those, defaulting to the
// last 30 days. Org-wide Viewer, like the audit trail it sits beside.
func (s *Server) auditEvidence(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if s.svc.Evidence == nil {
		http.Error(w, "evidence export is unavailable (config plane not mounted)", http.StatusNotFound)
		return
	}
	// Date inputs give yyyy-mm-dd; treat as UTC day boundaries. `to` is
	// exclusive-end friendly enough for an audit window, so bump it a day.
	parseDay := func(s string) (time.Time, bool) {
		t, err := time.Parse("2006-01-02", s)
		return t, err == nil
	}
	to := time.Now().UTC()
	if t, ok := parseDay(r.URL.Query().Get("to")); ok {
		to = t.AddDate(0, 0, 1)
	}
	from := to.AddDate(0, 0, -30)
	if t, ok := parseDay(r.URL.Query().Get("from")); ok {
		from = t
	}
	ev, err := s.svc.Evidence.Export(r.Context(), from, to)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Disposition",
		`attachment; filename="sextant-evidence-`+from.Format("20060102")+`-`+to.Format("20060102")+`.json"`)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(ev); err != nil {
		s.log.Error("evidence encode failed", "err", err)
	}
}

// postAssurance saves the organisation's approval controls (org Owner): the
// instelbare governance flows - four-eyes, require-change-request, and
// require-test-wave.
func (s *Server) postAssurance(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	a := fleet.Assurance{
		RequireFourEyes:      r.FormValue("requireFourEyes") != "",
		RequireChangeRequest: r.FormValue("requireChangeRequest") != "",
		RequireTestWave:      r.FormValue("requireTestWave") != "",
	}
	msg := fmt.Sprintf("assurance: four-eyes=%v change-request=%v test-wave=%v",
		a.RequireFourEyes, a.RequireChangeRequest, a.RequireTestWave)
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.SetAssurance(a), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
	return nil
}
