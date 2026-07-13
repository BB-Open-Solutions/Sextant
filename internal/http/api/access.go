package api

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// --- access bindings (per-scope RBAC, config-as-data) ---

// getAccess lists role bindings, narrowed to what the caller may view - a
// viewer bound to one group must not learn other groups' bindings, IdP group
// names or scopes (the visibility invariant every other read handler honours).
func (a *API) getAccess(w http.ResponseWriter, r *http.Request) error {
	writeJSON(w, http.StatusOK, a.cfg.Fleet().VisibleTo(a.canView(r)).Access)
	return nil
}

// postAccess grants (or updates) a role binding. Requires owner at the
// binding's scope: a group owner may delegate within the subtree, only an
// org owner grants org-wide.
func (a *API) postAccess(w http.ResponseWriter, r *http.Request) error {
	var in fleet.AccessBinding
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("access: grant %s %s at %s", in.Group, in.Role, in.Scope)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.Grant(in)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}

func (a *API) deleteAccess(w http.ResponseWriter, r *http.Request) error {
	var in struct {
		Group string `json:"group"`
		Scope string `json:"scope"`
	}
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := a.require(r, in.Scope, identity.Owner); err != nil {
		return err
	}
	msg := fmt.Sprintf("access: revoke %s at %s", in.Group, in.Scope)
	if err := a.cfg.Apply(r.Context(), rejectingMut(fleet.Revoke(in.Group, in.Scope)), msg, author(r)); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "applied"})
	return nil
}
