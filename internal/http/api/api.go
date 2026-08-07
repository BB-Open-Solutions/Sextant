// Package api serves /api/v1, the machine contract of Sextant: sxctl, CI,
// AI agents and any future frontend are all clients of this surface.
// Handlers are thin: decode -> one service call -> encode.
package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"reflect"
	"time"

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
	Prefs     ports.PrefsStore
	Directory ports.Directory
	Evidence  *app.EvidenceService
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
	prefs    ports.PrefsStore
	dir      ports.Directory
	evidence *app.EvidenceService
	authz    Authz
	token    string
	write    bool
	log      *slog.Logger
}

// now supplies time for stores that stamp writes.
func (a *API) now() time.Time { return time.Now() }

// New builds the API. Principals: a bearer token (service, owner
// everywhere) or a browser session (human, per-scope roles). No token and
// no session source disables the surface, so an unconfigured deployment
// exposes nothing by accident. write=false serves reads only.
func New(s Services, authz Authz, token string, write bool, log *slog.Logger) *API {
	return &API{cfg: s.Config, changes: s.Changes, rollouts: s.Rollouts,
		inv: s.Inventory, tokens: s.Tokens, devCreds: s.DevCreds, prefs: s.Prefs,
		dir: s.Directory, evidence: s.Evidence,
		authz: authz, token: token, write: write, log: log}
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
	// A path under /api/v1 that matches nothing answers in the same shape as
	// every other error (audit A3). Without this, a typo'd path gets Go's
	// default "404 page not found" in text/plain, and a client parsing the
	// documented {"error": ...} fails on the most ordinary mistake there is.
	//
	// Registered FIRST and left as the least specific pattern: Go's mux
	// prefers the most specific match, so every real route still wins. It is
	// deliberately not in the manifest - it is not an operation, and the
	// OpenAPI contract test would rightly object to documenting it.
	//
	// A METHOD mismatch (POST to a GET-only path) is still ServeMux's own
	// 405 in text/plain. That one cannot be overridden without routing every
	// method by hand, and it is a smaller trap: the path exists, so the
	// client is closer to right.
	mux.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{
			"error": "no such endpoint: " + r.Method + " " + r.URL.Path,
		})
	}))
	specRoutes(mux)

	get("/api/v1/me", a.getMe)
	get("/api/v1/me/preferences", a.getMyPrefs)
	rw("PUT", "/api/v1/me/preferences", a.putMyPrefs)
	get("/api/v1/audit", a.getAudit)
	get("/api/v1/evidence", a.getEvidence)
	get("/api/v1/directory/groups", a.getDirectoryGroups)

	get("/api/v1/fleet", a.getFleet)
	get("/api/v1/devices", a.getDevices)
	get("/api/v1/devices/{tag}", a.getDevice)
	get("/api/v1/hostkeys", a.getHostKeys)
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
	if a.devCreds != nil {
		rw("POST", "/api/v1/devices/{tag}/credential", a.postDeviceCredential)
	}
	rw("POST", "/api/v1/devices/{tag}/intent", a.postDeviceIntent)
	rw("DELETE", "/api/v1/devices/{tag}/intent", a.deleteDeviceIntent)

	rw("POST", "/api/v1/settings", a.postSetting)
	rw("DELETE", "/api/v1/settings", a.deleteSetting)
	rw("PUT", "/api/v1/policies/{id}", a.putPolicy)
	rw("DELETE", "/api/v1/policies/{id}", a.deletePolicy)
	rw("POST", "/api/v1/assignments", a.postAssignment)
	rw("DELETE", "/api/v1/assignments", a.deleteAssignment)
	rw("PUT", "/api/v1/filters/{id}", a.putFilter)
	rw("DELETE", "/api/v1/filters/{id}", a.deleteFilter)
	get("/api/v1/secret-refs", a.getSecretRefs)
	rw("POST", "/api/v1/secret-refs", a.postSecretRef)
	rw("DELETE", "/api/v1/secret-refs/{name}", a.deleteSecretRef)
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
			writeJSON(w, http.StatusForbidden, map[string]string{"error": "api disabled: no token or session auth configured"})
			return
		}
		p, ok := a.authenticate(r)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sextant"`)
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		if mutating {
			if !a.write {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "server is read-only (--write not set)"})
				return
			}
			if !p.verifyCSRF(r) {
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "missing or invalid X-CSRF-Token"})
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
				writeJSON(w, http.StatusForbidden, map[string]string{"error": "no role grants access"})
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
//
// Every error the v1 API can produce is {"error": "..."} in JSON, including
// the ones the wrapper raises before a handler runs. That used to be split:
// handler errors were JSON and the five middleware refusals were text/plain
// (audit A3, 2026-08-07). A client parsing the documented shape therefore
// succeeded on a 403 from a handler and failed on the 401 it meets first.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ports.ValidationError
	switch {
	case errors.As(err, new(*forbidden)):
		writeJSON(w, http.StatusForbidden, map[string]string{"error": err.Error()})
	case errors.As(err, &verr):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": verr.Detail})
	case errors.Is(err, ports.ErrConflict), errors.Is(err, app.ErrChangeRequestRequired):
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

// settingErr classifies a ConfigService.SetSetting/ClearSetting failure for the
// API. A gate rejection stays a 422 (ValidationError) and a missing-governance
// refusal stays a 409 (ErrChangeRequestRequired); every other reason is
// caller-fixable input - an unknown key, a wrong-typed value, or a dangling
// secret reference - so it maps to 400 rather than a 500.
func settingErr(err error) error {
	var verr *ports.ValidationError
	switch {
	case errors.As(err, &verr), errors.Is(err, app.ErrChangeRequestRequired):
		return err
	default:
		return reject(err)
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	// Several API responses carry one-shot secrets (tokens, device
	// credentials) that must never be cached - mirrors web.render.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(emptyList(v))
}

// emptyList turns a nil slice or map into an empty one, so a list endpoint
// answers [] and never null.
//
// Go marshals a nil slice as `null` and an initialised empty one as `[]`,
// which means the answer depends on whether the code path that built it
// happened to allocate. Measured on 2026-08-07: /api/v1/access answered
// `null` while /api/v1/secret-refs and /api/v1/hostkeys answered `[]`, from
// the same API, for the same "there is nothing here" situation.
//
// A client iterating the response therefore works against one endpoint and
// throws against another - and against the SAME endpoint depending on
// whether the fleet happens to be empty, which is the worst version: it
// works in every test and fails on a fresh deployment.
//
// Fixed here rather than per handler so a new list endpoint cannot get it
// wrong. Anything that is not a nil slice or map passes through untouched.
func emptyList(v any) any {
	if v == nil {
		return v
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Slice:
		if rv.IsNil() {
			return reflect.MakeSlice(rv.Type(), 0, 0).Interface()
		}
	case reflect.Map:
		if rv.IsNil() {
			return reflect.MakeMap(rv.Type()).Interface()
		}
	}
	return v
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
