// Package api serves /api/v1, the machine contract of Sextant: dfctl, CI,
// AI agents and any future frontend are all clients of this surface.
// Handlers are thin: decode -> one service call -> encode.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Services bundles the use-case services the API exposes. Changes,
// Rollouts and Inventory are optional: nil leaves their endpoints
// unregistered.
type Services struct {
	Config    *app.ConfigService
	Changes   *app.ChangeService
	Rollouts  *app.RolloutService
	Inventory *app.InventoryService
	Tokens    *app.TokenService
	DevCreds  *app.DeviceCredentials
}

// API is the /api/v1 handler group.
type API struct {
	manifest []string
	cfg      *app.ConfigService
	tokens   *app.TokenService
	devCreds *app.DeviceCredentials
	changes  *app.ChangeService
	rollouts *app.RolloutService
	inv      *app.InventoryService
	authz    Authz
	token    string
	write    bool
	log      *slog.Logger
}

// New builds the API. Principals: a bearer token (service, owner
// everywhere) or a browser session (human, per-scope roles). No token and
// no session source disables the surface, so an unconfigured deployment
// exposes nothing by accident. write=false serves reads only.
func New(s Services, authz Authz, token string, write bool, log *slog.Logger) *API {
	return &API{cfg: s.Config, changes: s.Changes, rollouts: s.Rollouts,
		inv: s.Inventory, tokens: s.Tokens, devCreds: s.DevCreds, authz: authz, token: token, write: write, log: log}
}

// Routes registers the API on mux.
func (a *API) Routes(mux *http.ServeMux) {
	get := func(p string, h func(http.ResponseWriter, *http.Request) error) {
		a.manifest = append(a.manifest, "GET "+p)
		mux.Handle("GET "+p, a.wrap(h, false))
	}
	rw := func(method, p string, h func(http.ResponseWriter, *http.Request) error) {
		a.manifest = append(a.manifest, method+" "+p)
		mux.Handle(method+" "+p, a.wrap(h, true))
	}
	specRoutes(mux)

	get("/api/v1/fleet", a.getFleet)
	get("/api/v1/devices", a.getDevices)
	get("/api/v1/devices/{tag}", a.getDevice)
	rw("POST", "/api/v1/devices", a.postDevice)
	rw("PATCH", "/api/v1/devices/{tag}", a.patchDevice)
	rw("DELETE", "/api/v1/devices/{tag}", a.deleteDevice)
	rw("POST", "/api/v1/devices/{tag}/retire", a.postDeviceRetire)
	rw("POST", "/api/v1/devices/{tag}/reactivate", a.postDeviceReactivate)
	rw("POST", "/api/v1/groups", a.postGroup)
	rw("PATCH", "/api/v1/groups/{name}", a.patchGroup)
	rw("DELETE", "/api/v1/groups/{name}", a.deleteGroup)
	rw("PUT", "/api/v1/apps", a.putApps)
	rw("PUT", "/api/v1/rollout/plan", a.putRolloutPlan)
	rw("PUT", "/api/v1/assurance", a.putAssurance)

	rw("POST", "/api/v1/settings", a.postSetting)
	rw("DELETE", "/api/v1/settings", a.deleteSetting)
	rw("PUT", "/api/v1/policies/{id}", a.putPolicy)
	rw("DELETE", "/api/v1/policies/{id}", a.deletePolicy)
	rw("POST", "/api/v1/assignments", a.postAssignment)
	rw("DELETE", "/api/v1/assignments", a.deleteAssignment)
	rw("PUT", "/api/v1/filters/{id}", a.putFilter)
	rw("DELETE", "/api/v1/filters/{id}", a.deleteFilter)
	get("/api/v1/access", a.getAccess)
	rw("POST", "/api/v1/access", a.postAccess)
	rw("DELETE", "/api/v1/access", a.deleteAccess)

	if a.changes != nil {
		get("/api/v1/changes", a.getChanges)
		get("/api/v1/changes/{id}", a.getChange)
		get("/api/v1/changes/{id}/diff", a.getChangeDiff)
		rw("POST", "/api/v1/changes", a.postChange)
		rw("POST", "/api/v1/changes/{id}/edits", a.postChangeEdit)
		rw("POST", "/api/v1/changes/{id}/submit", a.postChangeSubmit)
		rw("POST", "/api/v1/changes/{id}/merge", a.postChangeMerge)
		rw("POST", "/api/v1/changes/{id}/abandon", a.postChangeAbandon)
	}
	if a.rollouts != nil {
		get("/api/v1/rollout", a.getRollout)
		rw("POST", "/api/v1/rollout", a.postRollout)
		rw("POST", "/api/v1/rollout/tick", a.postRolloutTick)
		rw("DELETE", "/api/v1/rollout", a.deleteRollout)
	}
	if a.tokens != nil {
		get("/api/v1/tokens", a.getTokens)
		rw("POST", "/api/v1/tokens", a.postToken)
		rw("DELETE", "/api/v1/tokens/{id}", a.deleteToken)
	}
	if a.inv != nil {
		get("/api/v1/status", a.getStatusAll)
		get("/api/v1/status/{tag}", a.getStatus)
		get("/api/v1/facts/{tag}", a.getFacts)
	}
}

// wrap authenticates the principal (bearer token or session), guards
// mutations (write mode, CSRF for session users, viewer floor for reads),
// then maps handler errors onto HTTP statuses. Fine-grained per-scope
// authorization happens inside handlers via require().
func (a *API) wrap(h func(http.ResponseWriter, *http.Request) error, mutating bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" && a.authz.Sessions == nil && a.authz.Tokens == nil {
			http.Error(w, "api disabled: no token or session auth configured", http.StatusForbidden)
			return
		}
		p, ok := a.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sextant"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if mutating {
			if !a.write {
				http.Error(w, "server is read-only (--write not set)", http.StatusForbidden)
				return
			}
			if !p.verifyCSRF(r) {
				http.Error(w, "missing or invalid X-CSRF-Token", http.StatusForbidden)
				return
			}
		}
		r = r.WithContext(withPrincipal(r.Context(), p))
		// Reads require at least a role somewhere; scope-specific checks
		// happen in the handlers.
		if !mutating {
			pr := principalFrom(r.Context())
			rv := a.cfg.Fleet().IdentityResolver(
				a.authz.BaselineViewer, a.authz.BaselineEditor, a.authz.BaselineOwner)
			// A viewer-ceiling token still reads; a ceiling never blocks a
			// read the owner could do, so the view floor uses the owner.
			if !rv.CanViewAnything(pr.user) {
				http.Error(w, "no role grants access", http.StatusForbidden)
				return
			}
		}
		if err := h(w, r); err != nil {
			a.fail(w, r, err)
		}
	})
}

func bearerToken(r *http.Request) string {
	const prefix = "Bearer "
	h := r.Header.Get("Authorization")
	if len(h) > len(prefix) && h[:len(prefix)] == prefix {
		return h[len(prefix):]
	}
	return ""
}

// fail maps error kinds onto statuses: authorization 403, gate rejection
// 422, lost write race 409, dependency gap 503, bad input 400.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ports.ValidationError
	switch {
	case errors.As(err, new(*forbidden)):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": verr.Detail})
	case errors.Is(err, ports.ErrConflict):
		writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
	case errors.Is(err, ports.ErrUnavailable):
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
	case errors.As(err, new(*badRequest)):
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
	default:
		a.log.Error("api error", "method", r.Method, "path", r.URL.Path, "err", err)
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "internal error"})
	}
}

// badRequest marks caller errors (malformed body, unknown references).
type badRequest struct{ err error }

func (e *badRequest) Error() string { return e.err.Error() }
func (e *badRequest) Unwrap() error { return e.err }

func reject(err error) error { return &badRequest{err} }

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// decode parses a bounded JSON body.
func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return reject(err)
	}
	return nil
}
