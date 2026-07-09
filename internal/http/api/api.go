// Package api serves /api/v1, the machine contract of Sextant: dfctl, CI,
// AI agents and any future frontend are all clients of this surface.
// Handlers are thin: decode -> one service call -> encode.
package api

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Services bundles the use-case services the API exposes. Changes and
// Rollouts are optional: nil leaves their endpoints unregistered.
type Services struct {
	Config   *app.ConfigService
	Changes  *app.ChangeService
	Rollouts *app.RolloutService
}

// API is the /api/v1 handler group.
type API struct {
	cfg      *app.ConfigService
	changes  *app.ChangeService
	rollouts *app.RolloutService
	token    string
	write    bool
	log      *slog.Logger
}

// New builds the API. An empty token disables the whole surface (403), so
// an unconfigured deployment exposes nothing by accident. write=false
// serves reads only.
func New(s Services, token string, write bool, log *slog.Logger) *API {
	return &API{cfg: s.Config, changes: s.Changes, rollouts: s.Rollouts,
		token: token, write: write, log: log}
}

// Routes registers the API on mux.
func (a *API) Routes(mux *http.ServeMux) {
	get := func(p string, h func(http.ResponseWriter, *http.Request) error) {
		mux.Handle("GET "+p, a.wrap(h, false))
	}
	rw := func(method, p string, h func(http.ResponseWriter, *http.Request) error) {
		mux.Handle(method+" "+p, a.wrap(h, true))
	}

	get("/api/v1/fleet", a.getFleet)
	get("/api/v1/devices", a.getDevices)
	get("/api/v1/devices/{tag}", a.getDevice)

	rw("POST", "/api/v1/settings", a.postSetting)
	rw("DELETE", "/api/v1/settings", a.deleteSetting)
	rw("PUT", "/api/v1/policies/{id}", a.putPolicy)
	rw("DELETE", "/api/v1/policies/{id}", a.deletePolicy)
	rw("POST", "/api/v1/assignments", a.postAssignment)
	rw("DELETE", "/api/v1/assignments", a.deleteAssignment)
	rw("PUT", "/api/v1/filters/{id}", a.putFilter)
	rw("DELETE", "/api/v1/filters/{id}", a.deleteFilter)

	if a.changes != nil {
		get("/api/v1/changes", a.getChanges)
		get("/api/v1/changes/{id}", a.getChange)
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
}

// wrap enforces bearer auth (and write mode for mutating routes), then maps
// handler errors onto HTTP statuses.
func (a *API) wrap(h func(http.ResponseWriter, *http.Request) error, mutating bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if a.token == "" {
			http.Error(w, "api disabled: no token configured", http.StatusForbidden)
			return
		}
		got := bearerToken(r)
		if subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) != 1 {
			w.Header().Set("WWW-Authenticate", `Bearer realm="sextant"`)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if mutating && !a.write {
			http.Error(w, "server is read-only (--write not set)", http.StatusForbidden)
			return
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

// fail maps error kinds onto statuses: gate rejection 422, lost write race
// 409, bad input 400.
func (a *API) fail(w http.ResponseWriter, r *http.Request, err error) {
	var verr *ports.ValidationError
	switch {
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

// author derives commit attribution for API writes. Until OIDC lands
// (phase 5), API clients identify as the service account.
func author(*http.Request) ports.Author {
	return ports.Author{Name: "sextant-api", Email: "api@sextant"}
}
