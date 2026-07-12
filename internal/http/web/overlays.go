package web

import (
	"net/http"
	"net/url"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// overlays.go: the custom-overlay surface (ADR 0014). An owner authors Nix
// overlay modules (overlays/<name>.nix) in a code editor; each save passes the
// Nix eval gate before it commits, so a module that does not build never
// reaches git. Scopes then select overlays through the apps/overlays picker.
// Authoring is owner-only: an overlay is arbitrary code the generator imports.

// overlaysPage lists the overlays and shows one in the editor (?name=).
func (s *Server) overlaysPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	names, err := s.svc.Config.ListOverlays()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	data := map[string]any{"Title": "Overlays", "Nav": "overlays", "Overlays": names}

	if name := strings.TrimSpace(r.URL.Query().Get("name")); name != "" {
		code, err := s.svc.Config.ReadOverlay(name)
		if err != nil {
			data["Error"] = "overlay not found: " + name
		} else {
			data["Selected"], data["Code"] = name, code
		}
	}
	s.render(w, "overlays", data, v)
}

// postOverlayWrite creates or replaces an overlay. A gate rejection (the module
// does not evaluate) surfaces to the operator; nothing is committed.
func (s *Server) postOverlayWrite(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	code := r.FormValue("code")
	if err := s.svc.Config.WriteOverlay(r.Context(), name, code, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/overlays?name="+url.QueryEscape(name), http.StatusSeeOther)
	return nil
}

// postOverlayRemove deletes an overlay. The gate then confirms no scope still
// selects it (a dangling reference would fail the build).
func (s *Server) postOverlayRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if err := s.svc.Config.DeleteOverlay(r.Context(), r.PathValue("name"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/overlays", http.StatusSeeOther)
	return nil
}
