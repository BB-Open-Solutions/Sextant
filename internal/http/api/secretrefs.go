package api

// secretrefs.go: registering the NAMES of secrets over the API.
//
// WHY IT EXISTS. Everything else about a secret is already scriptable:
// scripts/rekey-secrets.sh creates the encrypted material and hands it to the
// right recipient set. Registering the name it is known by was the one step
// that had no path but a browser form, so an onboarding script could produce a
// secret the console would then refuse to reference ("unknown secret
// reference; register it first"). Found while enabling the local administrator
// from the CLI, 2026-08-04.
//
// Sextant only ever knows NAMES. The material lives in agenix on the device
// and in the cluster's own secret store; nothing here carries a value, which
// is why registration is cheap and removal is not.

import (
	"net/http"
	"sort"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// getSecretRefs lists the registered names with their descriptions.
func (a *API) getSecretRefs(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	f := a.cfg.Fleet()
	type row struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	out := make([]row, 0, len(f.SecretRefs))
	for name, ref := range f.SecretRefs {
		out = append(out, row{Name: name, Description: ref.Description})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, http.StatusOK, out)
	return nil
}

// postSecretRef registers a name.
//
// Structural rather than gated, matching the console: a newly registered name
// is by definition referenced by nothing, so no device's generated
// configuration changes and there is nothing for the nix gate to prove.
// Waiting out an evaluation here made the row appear to vanish (operator
// report, 2026-07-29).
func (a *API) postSecretRef(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Name        string `json:"name"`
		Description string `json:"description,omitempty"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	name := strings.TrimSpace(in.Name)
	ref := fleet.SecretRef{Description: strings.TrimSpace(in.Description)}
	if err := a.cfg.ApplyStructural(r.Context(), fleet.AddSecretRef(name, ref),
		"secrets: register reference "+name, author(r)); err != nil {
		return reject(err)
	}
	writeJSON(w, http.StatusCreated, map[string]string{"name": name})
	return nil
}

// deleteSecretRef unregisters a name. Gated, unlike registration: a setting
// still pointing at it breaks the build, and the gate is what says so rather
// than a device discovering it at activation.
func (a *API) deleteSecretRef(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Owner); err != nil {
		return err
	}
	name := r.PathValue("name")
	if err := a.cfg.Apply(r.Context(), fleet.RemoveSecretRef(name),
		"secrets: remove reference "+name, author(r)); err != nil {
		return settingErr(err)
	}
	writeJSON(w, http.StatusOK, map[string]string{"name": name})
	return nil
}
