package web

import (
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// erasure.go: the console surface for a GDPR art. 17 request.
//
// Two acts, not one. The preview is a GET-shaped POST that removes nothing
// and reports what would go; only a second submit, carrying the same two
// identifiers back, actually erases. Same shape as arming a wipe: the
// irreversible thing is reachable, and reaching it is deliberate.
//
// The page always renders what CANNOT be removed, on both steps. An operator
// who answers a data subject does it from this page, and the honest answer
// includes the git history.

// erasurePage shows the form. Owner-only: this is the most destructive
// operation in the console that is not a wipe, and unlike a wipe it cannot
// be undone by re-imaging.
func (s *Server) erasurePage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	s.render(w, "erasure", s.erasureData(v, nil), v)
}

// erasureData builds the page, optionally with a report.
func (s *Server) erasureData(v view, data map[string]any) map[string]any {
	if data == nil {
		data = map[string]any{}
	}
	data["Title"] = v.L.T("erasure.title")
	data["Nav"] = "org"
	data["Unavailable"] = s.svc.Erasure == nil
	return data
}

// postErasurePreview reports what an erasure would remove. Removes nothing.
func (s *Server) postErasurePreview(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.Erasure == nil {
		return errErasureUnavailable
	}
	subject := strings.TrimSpace(r.FormValue("subject"))
	username := strings.TrimSpace(r.FormValue("username"))
	rep, err := s.svc.Erasure.Preview(r.Context(), subject, username)
	if err != nil {
		return err
	}
	s.render(w, "erasure", s.erasureData(v, map[string]any{
		"Report": rep, "Subject": subject, "Username": username,
	}), v)
	return nil
}

// postErasureConfirm performs it. The identifiers come back from the form
// rather than from a session: what is erased must be what the operator just
// read on the preview, not something a stale server-side value decided.
func (s *Server) postErasureConfirm(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.Erasure == nil {
		return errErasureUnavailable
	}
	subject := strings.TrimSpace(r.FormValue("subject"))
	username := strings.TrimSpace(r.FormValue("username"))
	rep, err := s.svc.Erasure.Erase(r.Context(), subject, username, webAuthor(v))
	if err != nil {
		return err
	}
	s.render(w, "erasure", s.erasureData(v, map[string]any{
		"Report": rep, "Done": true,
	}), v)
	return nil
}

var errErasureUnavailable = erasureUnavailable{}

type erasureUnavailable struct{}

func (erasureUnavailable) Error() string {
	return "erasure needs the database (postgres not configured)"
}
