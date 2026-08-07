package api

import (
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// directory.go: read-only IdP group browse for access-binding pickers.
// The login IdP (OIDC) and the group source (LDAP) may be different
// systems; this surface only ever lists, never manages.

const errDirectoryUnavailable = simpleErr("directory browse not configured (LDAP)")

// getDirectoryGroups lists IdP groups matching ?q=. Granting a binding
// requires Owner at some scope, so browsing the directory does too - group
// names are organisational structure, not viewer material.
func (a *API) getDirectoryGroups(w http.ResponseWriter, r *http.Request) error {
	if a.dir == nil {
		return &forbidden{errDirectoryUnavailable}
	}
	if !a.ownsAnywhere(r) {
		return &forbidden{simpleErr("directory browse requires owner at some scope")}
	}
	groups, err := a.dir.ListGroups(r.Context(), r.URL.Query().Get("q"))
	if err != nil {
		return err
	}
	return writeList(w, r, groups)
}

// ownsAnywhere reports whether the principal holds Owner at org or at any
// group (ceiling applied).
func (a *API) ownsAnywhere(r *http.Request) bool {
	p := principalFrom(r.Context())
	f := a.cfg.Fleet()
	rv := f.IdentityResolver(a.authz.BaselineViewer, a.authz.BaselineEditor, a.authz.BaselineOwner)
	clamp := func(got identity.Role) identity.Role {
		if p.hasCap && p.ceiling < got {
			return p.ceiling
		}
		return got
	}
	if clamp(rv.RoleAt(p.user, "org")) >= identity.Owner {
		return true
	}
	for g := range f.Groups {
		if clamp(rv.RoleAt(p.user, "group:"+g)) >= identity.Owner {
			return true
		}
	}
	return false
}
