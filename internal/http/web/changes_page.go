package web

import (
	"net/http"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

func (s *Server) changesPage(w http.ResponseWriter, r *http.Request, v view) {
	// Diffs expose every scope: org-wide read required.
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	crs, err := s.svc.Changes.List(r.Context())
	f := s.svc.Config.Fleet()
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	data := map[string]any{"Title": "Changes", "Nav": "changes", "Changes": crs,
		"Groups":  groups,
		"CanEdit": v.roleAt("org").Meets(identity.Editor)}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "changes", data, v)
}

// diffPage shows an approver what a change would apply.
func (s *Server) diffPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	id := r.PathValue("id")
	cr, ok, err := s.svc.Changes.Get(r.Context(), id)
	if err != nil || !ok {
		http.NotFound(w, r)
		return
	}
	diff, err := s.svc.Changes.Diff(r.Context(), id)
	data := map[string]any{"Title": "Diff " + id, "Nav": "changes",
		"ID": id, "Change": cr, "Diff": diff}
	if err != nil {
		data["Error"] = err.Error()
	}
	s.render(w, "diff", data, v)
}

func (s *Server) postChange(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	_, err := s.svc.Changes.Open(r.Context(), r.FormValue("id"), r.FormValue("title"), webAuthor(v))
	if err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeSubmit(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Submit(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeMerge(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Merge(r.Context(), r.PathValue("id"), webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}

func (s *Server) postChangeAbandon(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	if _, err := s.svc.Changes.Abandon(r.Context(), r.PathValue("id")); err != nil {
		return err
	}
	http.Redirect(w, r, "/pipeline", http.StatusSeeOther)
	return nil
}
