package web

// access_ops.go: the console's access-control surface - the role-bindings page
// and the grant/revoke actions. Split out of pages.go to keep each file
// cohesive.

import (
	"fmt"
	"net/http"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

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
		"CanOwn":          canOwn,
		"FourEyes":        f.Assurance != nil && f.Assurance.RequireFourEyes,
		"RequireChange":   f.Assurance != nil && f.Assurance.RequireChangeRequest,
		"RequireTestWave": f.Assurance != nil && f.Assurance.RequireTestWave,
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

func (s *Server) postAccessGrant(w http.ResponseWriter, r *http.Request, v view) error {
	b := fleet.AccessBinding{Group: r.FormValue("group"),
		Role: r.FormValue("role"), Scope: r.FormValue("scope")}
	if err := s.requireWeb(v, b.Scope, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("access: grant %s %s at %s", b.Group, b.Role, b.Scope)
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.Grant(b), msg, webAuthor(v)); err != nil {
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
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.Revoke(group, scope), msg, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/access", http.StatusSeeOther)
	return nil
}
