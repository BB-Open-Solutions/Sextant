package web

import (
	"fmt"
	"net/http"
	"strconv"

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

// postAssurance toggles the four-eyes control (org Owner).
func (s *Server) postAssurance(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	on := r.FormValue("requireFourEyes") != ""
	msg := fmt.Sprintf("assurance: four-eyes %v", on)
	if err := s.svc.Config.Apply(r.Context(),
		fleet.SetAssurance(fleet.Assurance{RequireFourEyes: on}), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
	return nil
}
