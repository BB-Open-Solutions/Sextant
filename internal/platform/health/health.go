// Package health provides liveness and readiness endpoints. Liveness
// (/healthz) answers "is the process alive" and always returns 200. Readiness
// (/readyz) runs registered dependency checks (git repo reachable, database
// reachable) and returns 503 when any fails, so an orchestrator stops routing
// traffic to a pod that cannot do useful work.
package health

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Check reports whether one dependency is usable. It must respect ctx.
type Check func(ctx context.Context) error

// Registry holds named readiness checks. Zero value is not usable; use New.
type Registry struct {
	mu      sync.RWMutex
	checks  map[string]Check
	timeout time.Duration
}

// New returns a Registry whose checks each get the given timeout per probe.
func New(timeout time.Duration) *Registry {
	return &Registry{checks: make(map[string]Check), timeout: timeout}
}

// Register adds a named readiness check. Registering the same name twice
// replaces the previous check.
func (r *Registry) Register(name string, c Check) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.checks[name] = c
}

// Liveness always reports 200: the process is up and serving.
func (r *Registry) Liveness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
}

// Readiness runs every registered check and reports 200 only when all pass.
// The response body lists per-check status as JSON for debuggability.
func (r *Registry) Readiness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		r.mu.RLock()
		checks := make(map[string]Check, len(r.checks))
		for n, c := range r.checks {
			checks[n] = c
		}
		timeout := r.timeout
		r.mu.RUnlock()

		result := make(map[string]string, len(checks))
		ready := true
		for name, check := range checks {
			ctx, cancel := context.WithTimeout(req.Context(), timeout)
			err := check(ctx)
			cancel()
			if err != nil {
				result[name] = err.Error()
				ready = false
			} else {
				result[name] = "ok"
			}
		}

		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Ready  bool              `json:"ready"`
			Checks map[string]string `json:"checks"`
		}{ready, result})
	})
}
