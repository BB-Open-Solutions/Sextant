package web

import (
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// forge.go: the account the console itself pushes to the forge with
// (ADR 0022). Owner-only, and the one page in the console whose purpose is to
// let an admin replace a credential without touching the cluster.
//
// The current token is never sent to the browser. This page can say which
// account is in use, when it was last replaced and by whom, and it can put a
// new one in - which is everything a rotation needs and nothing more.

// forgePage shows the current forge account and the rotation form.
func (s *Server) forgePage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	data := map[string]any{"Title": "Forge account", "Nav": "org"}
	if s.svc.ForgeID == nil || !s.svc.ForgeID.Enabled() {
		// Say WHICH precondition is missing rather than "unavailable": one is
		// fixed by setting a key, the other by giving the pod a home.
		data["Unavailable"] = true
		s.render(w, "forge", data, v)
		return
	}
	id, ok, err := s.svc.ForgeID.Current(r.Context())
	if err != nil {
		s.log.Warn("forge identity load failed", "err", err)
		data["Error"] = v.L.T("forge.load_failed")
	}
	data["Configured"] = ok
	data["Identity"] = id
	data["Saved"] = r.URL.Query().Get("saved") == "1"
	s.render(w, "forge", data, v)
}

// postForgeSave stores and applies a new forge credential. Owner-only.
func (s *Server) postForgeSave(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.ForgeID == nil {
		return errForgeUnavailable
	}
	host := strings.TrimSpace(r.FormValue("host"))
	username := strings.TrimSpace(r.FormValue("username"))
	// The token is NOT trimmed of interior content and not logged; it is
	// passed straight through. Leading/trailing whitespace comes from a paste
	// and is never meant, so that much is trimmed.
	token := strings.TrimSpace(r.FormValue("token"))
	if err := s.svc.ForgeID.Set(r.Context(), host, username, token, webAuthor(v).Name); err != nil {
		return err
	}
	http.Redirect(w, r, "/org/forge?saved=1", http.StatusSeeOther)
	return nil
}

// postForgeClear drops the stored credential, returning the deployment to
// whatever its deployment mounts. Owner-only.
func (s *Server) postForgeClear(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.ForgeID == nil {
		return errForgeUnavailable
	}
	if err := s.svc.ForgeID.Clear(r.Context(), webAuthor(v).Name); err != nil {
		return err
	}
	http.Redirect(w, r, "/org/forge", http.StatusSeeOther)
	return nil
}

// errForgeUnavailable is returned rather than a nil dereference when the
// service is not wired (no Postgres).
var errForgeUnavailable = forgeUnavailable{}

type forgeUnavailable struct{}

func (forgeUnavailable) Error() string {
	return "the forge account needs the database (postgres not configured)"
}
