package api

import (
	"net/http"
	"sort"
	"strconv"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// me.go: the self surface - who am I, what may I do where, my preferences,
// plus the audit trail. This feeds the profile menu and personal settings.

const errPrefsUnavailable = simpleErr("preferences need the database (postgres not configured)")

// getMe reports the caller's identity and effective role per visible scope.
// Roles are derived live from the current document, never stored.
func (a *API) getMe(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	f := a.cfg.Fleet()
	rv := f.IdentityResolver(a.authz.BaselineViewer, a.authz.BaselineEditor, a.authz.BaselineOwner)

	clamp := func(got identity.Role) identity.Role {
		if p.hasCap && p.ceiling < got {
			return p.ceiling
		}
		return got
	}
	roles := map[string]string{}
	if role := clamp(rv.RoleAt(p.user, "org")); role >= identity.Viewer {
		roles["org"] = role.String()
	}
	groups := make([]string, 0, len(f.Groups))
	for g := range f.Groups {
		groups = append(groups, g)
	}
	sort.Strings(groups)
	for _, g := range groups {
		if role := clamp(rv.RoleAt(p.user, "group:"+g)); role >= identity.Viewer {
			roles["group:"+g] = role.String()
		}
	}
	out := map[string]any{
		"subject": p.user.Subject,
		"name":    p.user.Name,
		"email":   p.user.Email,
		"groups":  p.user.Groups,
		"service": p.user.Service,
		"roles":   roles,
	}
	if p.hasCap {
		out["ceiling"] = p.ceiling.String()
	}
	writeJSON(w, http.StatusOK, out)
	return nil
}

// getMyPrefs returns the caller's stored preferences (empty = org default).
func (a *API) getMyPrefs(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	if a.prefs == nil {
		return &forbidden{errPrefsUnavailable}
	}
	prefs, _, err := a.prefs.GetPrefs(r.Context(), app.DefaultTenant, p.user.Subject)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, prefs)
	return nil
}

// putMyPrefs stores the caller's preferences after validation.
func (a *API) putMyPrefs(w http.ResponseWriter, r *http.Request) error {
	p := principalFrom(r.Context())
	if a.prefs == nil {
		return &forbidden{errPrefsUnavailable}
	}
	var in identity.Preferences
	if err := decode(r, &in); err != nil {
		return err
	}
	if err := in.Validate(); err != nil {
		return &ports.ValidationError{Detail: err.Error()}
	}
	if err := a.prefs.PutPrefs(r.Context(), app.DefaultTenant, p.user.Subject, in, a.now()); err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, in)
	return nil
}

// getAudit returns the config commit trail. Commits span every scope, so
// org-wide Viewer is required, like change diffs.
func (a *API) getAudit(w http.ResponseWriter, r *http.Request) error {
	if err := a.require(r, "org", identity.Viewer); err != nil {
		return err
	}
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	entries, err := a.cfg.AuditLog(r.Context(), limit)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, entries)
	return nil
}

// postDeviceCredential re-issues a device credential (lost secret,
// re-image). The old credential stops working; the new one is shown once.
func (a *API) postDeviceCredential(w http.ResponseWriter, r *http.Request) error {
	tag := r.PathValue("tag")
	if err := a.require(r, "device:"+tag, identity.Editor); err != nil {
		return err
	}
	f := a.cfg.Fleet()
	d, ok := f.Devices[tag]
	if !ok || !a.canView(r)("device:"+tag) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "unknown device " + tag})
		return nil
	}
	if d.Retired() {
		return &ports.ValidationError{Detail: "device is retired; reactivate it first"}
	}
	secret, err := a.devCreds.Issue(r.Context(), tag)
	if err != nil {
		return err
	}
	writeJSON(w, http.StatusOK, map[string]string{
		"credential": secret,
		"notice":     "store this device credential now; it is not shown again",
	})
	return nil
}
