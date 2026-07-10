package api

import (
	"net/http"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// evidence.go: the audit-evidence export. Spans every scope, so org-wide
// Viewer, like the audit trail and change diffs.

// getEvidence exports the bundle for ?from=...&to=... (RFC 3339; to
// defaults to now, from to 30 days before to).
func (a *API) getEvidence(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	if a.evidence == nil {
		return &forbidden{simpleErr("evidence export needs the change store (config plane not mounted)")}
	}
	to := a.now()
	if v := r.URL.Query().Get("to"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return &ports.ValidationError{Detail: "to: RFC 3339 expected (2026-07-01T00:00:00Z)"}
		}
		to = t
	}
	from := to.AddDate(0, 0, -30)
	if v := r.URL.Query().Get("from"); v != "" {
		t, err := time.Parse(time.RFC3339, v)
		if err != nil {
			return &ports.ValidationError{Detail: "from: RFC 3339 expected (2026-07-01T00:00:00Z)"}
		}
		from = t
	}
	ev, err := a.evidence.Export(r.Context(), from, to)
	if err != nil {
		return reject(err)
	}
	// An export is a document: name it so a browser download is traceable.
	w.Header().Set("Content-Disposition",
		`attachment; filename="sextant-evidence-`+from.Format("20060102")+`-`+to.Format("20060102")+`.json"`)
	writeJSON(w, http.StatusOK, ev)
	return nil
}
