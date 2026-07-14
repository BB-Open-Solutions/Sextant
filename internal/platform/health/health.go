// Package health provides liveness and readiness endpoints. Liveness
// (/healthz) answers "is the process alive" and always returns 200. Readiness
// (/readyz) runs registered dependency checks (git repo reachable, database
// reachable) and returns 503 when any fails, so an orchestrator stops routing
// traffic to a pod that cannot do useful work.
package health

import (
	"context"
	"encoding/json"
	"html/template"
	"log/slog"
	"net/http"
	"sort"
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
	log     *slog.Logger
}

// New returns a Registry whose checks each get the given timeout per probe.
func New(timeout time.Duration) *Registry {
	return &Registry{checks: make(map[string]Check), timeout: timeout, log: slog.Default()}
}

// SetLogger wires the real application logger (New defaults to
// slog.Default()). Used to record the full detail of a failing dependency
// check server-side, since /readyz and /status are unauthenticated and must
// not echo that detail to the caller (see Snapshot).
func (r *Registry) SetLogger(log *slog.Logger) {
	if log == nil {
		log = slog.Default()
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.log = log
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

// CheckResult is one dependency's outcome: OK, or the reason it failed.
type CheckResult struct {
	Name string `json:"name"`
	OK   bool   `json:"ok"`
	Info string `json:"info,omitempty"` // "ok" or the error text
}

// Snapshot runs every registered check once, concurrently, and returns the
// overall readiness plus a per-check result, sorted by name so the output is
// stable. Checks run concurrently (each still bounded by its own per-check
// timeout) so an N-dependency probe costs max(check latency), not
// sum(check latency) - sequential checks could otherwise make /readyz take
// N times the per-check timeout, itself exceeding an orchestrator's probe
// timeout and causing readiness to flap.
//
// A failing check's Info is deliberately generic ("unavailable"), never
// err.Error(): /readyz and /status are unauthenticated (an orchestrator and
// operators must reach them even when login itself is down), and a
// dependency error routinely embeds internals - a Postgres DSN's host/user,
// a git remote URL, an OIDC issuer detail - that would hand an unauthenticated
// caller free reconnaissance during an outage. The full error is logged
// server-side instead.
func (r *Registry) Snapshot(ctx context.Context) (bool, []CheckResult) {
	r.mu.RLock()
	checks := make(map[string]Check, len(r.checks))
	for n, c := range r.checks {
		checks[n] = c
	}
	timeout, log := r.timeout, r.log
	r.mu.RUnlock()

	names := make([]string, 0, len(checks))
	for n := range checks {
		names = append(names, n)
	}
	sort.Strings(names)

	out := make([]CheckResult, len(names))
	var wg sync.WaitGroup
	for i, name := range names {
		wg.Add(1)
		go func(i int, name string, check Check) {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, timeout)
			err := check(cctx)
			cancel()
			res := CheckResult{Name: name, OK: err == nil, Info: "ok"}
			if err != nil {
				log.Warn("readiness check failed", "check", name, "err", err)
				res.Info = "unavailable"
			}
			out[i] = res
		}(i, name, checks[name])
	}
	wg.Wait()

	ready := true
	for _, res := range out {
		if !res.OK {
			ready = false
		}
	}
	return ready, out
}

// Readiness runs every registered check and reports 200 only when all pass.
// The response body lists per-check status as JSON for debuggability.
func (r *Registry) Readiness() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ready, results := r.Snapshot(req.Context())
		checks := make(map[string]string, len(results))
		for _, c := range results {
			checks[c.Name] = c.Info
		}
		w.Header().Set("Content-Type", "application/json")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = json.NewEncoder(w).Encode(struct {
			Ready  bool              `json:"ready"`
			Checks map[string]string `json:"checks"`
		}{ready, checks})
	})
}

// StatusPage renders the readiness snapshot as a themed HTML page for humans -
// the console footer's "System status" link. It links the app stylesheet for
// the shared look; no inline style or script, so it stays CSP-clean. Served
// unauthenticated (like the probes), so ops can read it even when the login
// path or database is down.
func (r *Registry) StatusPage() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		ready, results := r.Snapshot(req.Context())
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		if !ready {
			w.WriteHeader(http.StatusServiceUnavailable)
		}
		_ = statusTmpl.Execute(w, struct {
			Ready   bool
			Results []CheckResult
		}{ready, results})
	})
}

// statusTmpl is the System status page. Kept minimal and self-contained; it
// uses the same utility classes app.css already ships.
var statusTmpl = template.Must(template.New("status").Parse(`<!DOCTYPE html>
<html lang="en" class="light"><head>
<meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>System status - Sextant</title>
<link rel="icon" type="image/svg+xml" href="/static/favicon.svg">
<link rel="stylesheet" href="/static/fonts.css"><link rel="stylesheet" href="/static/app.css">
</head><body class="bg-surface text-on-surface antialiased">
<div class="mx-auto max-w-2xl px-6 py-16">
  <div class="mb-8 flex items-center gap-3">
    <span class="flex h-10 w-10 items-center justify-center rounded-lg {{if .Ready}}bg-secondary-container text-on-secondary-container{{else}}bg-error-container text-on-error-container{{end}}">
      <span class="material-symbols-outlined">{{if .Ready}}check_circle{{else}}error{{end}}</span></span>
    <div>
      <h1 class="mb-0 text-headline-sm">System status</h1>
      <p class="mb-0 text-body-md text-text-tertiary">{{if .Ready}}All systems operational{{else}}A dependency is degraded{{end}}</p>
    </div>
  </div>
  <div class="overflow-hidden rounded-lg border border-border-hairline bg-canvas shadow-sm">
    {{range .Results}}
    <div class="flex items-center justify-between border-b border-border-soft px-5 py-4 last:border-0">
      <span class="font-medium capitalize text-ink">{{.Name}}</span>
      {{if .OK}}<span class="tag ok"><span class="material-symbols-outlined msz-14">check_circle</span>operational</span>
      {{else}}<span class="tag bad" title="{{.Info}}"><span class="material-symbols-outlined msz-14">warning</span>degraded</span>{{end}}
    </div>
    {{else}}<div class="px-5 py-4 text-text-tertiary">No checks registered.</div>{{end}}
  </div>
  <p class="mt-6 text-label-md text-text-tertiary"><a href="/" class="text-secondary">Back to Sextant</a> - machine-readable at <a href="/readyz" class="text-secondary">/readyz</a></p>
</div></body></html>`))
