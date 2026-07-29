package web

import (
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// secrets.go: the secret-reference registry surface. The org registers the
// NAMES of secrets that live in the real store (agenix on the device, a Secret
// in the cluster); secret-typed settings reference a name. Sextant never holds
// a secret value, so nothing here takes one - only names and descriptions.

type secretRefRow struct {
	Name        string
	Description string
}

// secretsPage lists the registered secret references and (for owners) the form
// to register a new one. Org Viewer to see; register/remove are owner-gated.
func (s *Server) secretsPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	f := s.svc.Config.Fleet()
	rows := make([]secretRefRow, 0, len(f.SecretRefs))
	for name, ref := range f.SecretRefs {
		rows = append(rows, secretRefRow{Name: name, Description: ref.Description})
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].Name < rows[j].Name })
	s.render(w, "secrets", map[string]any{
		"Title": "Secrets", "Nav": "secrets",
		"Refs": rows,
		// Prefill the create form when a secret field deep-links here with a
		// suggested name (?name=), so registering a reference is one click.
		"Prefill": slugify(r.URL.Query().Get("name")),
		"CanOwn":  v.roleAt("org").Meets(identity.Owner),
	}, v)
}

// postSecretRegister registers a secret reference name (config-as-data, an
// audited commit). Org Owner. Only the name is stored - never a value.
func (s *Server) postSecretRegister(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := strings.TrimSpace(r.FormValue("name"))
	ref := fleet.SecretRef{Description: strings.TrimSpace(r.FormValue("description"))}
	// Structural, not gated: a newly registered name is by definition
	// referenced by nothing, so no device's generated config changes - there
	// is nothing for the nix gate to prove, and the operator sees the row
	// immediately instead of waiting out a background eval (the row seemed
	// to vanish, operator report 2026-07-29). REMOVAL stays gated: a name a
	// setting still points at breaks builds, and the gate is what says so.
	if err := s.svc.Config.ApplyStructural(r.Context(), fleet.AddSecretRef(name, ref),
		"secrets: register reference "+name, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/secrets", http.StatusSeeOther)
	return nil
}

// postSecretRemove unregisters a secret reference name. A setting still
// pointing at it will fail the gate, surfacing the dangling reference.
func (s *Server) postSecretRemove(w http.ResponseWriter, r *http.Request, v view) error {
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := s.applyGated(r, v, fleet.RemoveSecretRef(name),
		"secrets: remove reference "+name); err != nil {
		return err
	}
	http.Redirect(w, r, "/secrets", http.StatusSeeOther)
	return nil
}
