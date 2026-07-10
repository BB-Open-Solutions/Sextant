// Package web is the human console: server-rendered pages over the same app
// services the JSON API uses. Handlers are thin - parse, one service call,
// render - and every mutation carries a CSRF token and per-scope RBAC.
package web

import (
	"crypto/subtle"
	"embed"
	"html/template"
	"log/slog"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

//go:embed templates/*.html static/*
var assets embed.FS

// Sessions provides the authenticated user for a request (the oidc
// adapter, or a dev stub on loopback).
type Sessions interface {
	SessionUser(r *http.Request) (identity.User, string, bool)
}

// Services are the app services the console renders.
type Services struct {
	Config    *app.ConfigService
	Changes   *app.ChangeService
	Rollouts  *app.RolloutService
	Inventory *app.InventoryService
}

// Server renders the console.
type Server struct {
	svc      Services
	sessions Sessions
	tmpl     map[string]*template.Template
	log      *slog.Logger
	write    bool

	baseViewer, baseEditor, baseOwner []string
}

// New builds the console server. Baselines mirror the API's org-wide role
// groups.
func New(svc Services, sessions Sessions, write bool,
	baseViewer, baseEditor, baseOwner []string, log *slog.Logger) (*Server, error) {
	pages := []string{"overview", "devices", "device", "policies", "changes", "rollout", "access"}
	tmpl := make(map[string]*template.Template, len(pages)+1)
	for _, p := range pages {
		t, err := template.ParseFS(assets, "templates/layout.html", "templates/"+p+".html")
		if err != nil {
			return nil, err
		}
		tmpl[p] = t
	}
	login, err := template.ParseFS(assets, "templates/login.html")
	if err != nil {
		return nil, err
	}
	tmpl["login"] = login
	return &Server{svc: svc, sessions: sessions, tmpl: tmpl, log: log, write: write,
		baseViewer: baseViewer, baseEditor: baseEditor, baseOwner: baseOwner}, nil
}

// Routes registers the console.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", http.FileServerFS(assets))
	mux.HandleFunc("GET /login", s.handleLoginPage)

	get := func(p string, h func(http.ResponseWriter, *http.Request, view)) {
		mux.Handle("GET "+p, s.page(h))
	}
	post := func(p string, h func(http.ResponseWriter, *http.Request, view) error) {
		mux.Handle("POST "+p, s.action(h))
	}

	get("/{$}", s.overview)
	get("/devices", s.devices)
	get("/devices/{tag}", s.device)
	get("/policies", s.policies)
	get("/changes", s.changesPage)
	get("/rollout", s.rolloutPage)
	get("/access", s.accessPage)

	post("/devices/{tag}/settings", s.postDeviceSetting)
	post("/changes", s.postChange)
	post("/changes/{id}/submit", s.postChangeSubmit)
	post("/changes/{id}/merge", s.postChangeMerge)
	post("/changes/{id}/abandon", s.postChangeAbandon)
	post("/rollout", s.postRolloutStart)
	post("/rollout/tick", s.postRolloutTick)
	post("/rollout/cancel", s.postRolloutCancel)
	post("/access/grant", s.postAccessGrant)
	post("/access/revoke", s.postAccessRevoke)
}

// view is the per-request context every page gets.
type view struct {
	User identity.User
	CSRF string
	rv   identity.Resolver
}

func (v view) roleAt(ref string) identity.Role { return v.rv.RoleAt(v.User, ref) }

// authed resolves the session or sends the visitor to /login.
func (s *Server) authed(w http.ResponseWriter, r *http.Request) (view, bool) {
	if s.sessions == nil {
		http.Redirect(w, r, "/login", http.StatusFound)
		return view{}, false
	}
	u, csrf, ok := s.sessions.SessionUser(r)
	if !ok {
		http.Redirect(w, r, "/login", http.StatusFound)
		return view{}, false
	}
	rv := s.svc.Config.Fleet().IdentityResolver(s.baseViewer, s.baseEditor, s.baseOwner)
	if !rv.CanViewAnything(u) {
		http.Error(w, "no role grants console access to your account", http.StatusForbidden)
		return view{}, false
	}
	return view{User: u, CSRF: csrf, rv: rv}, true
}

// page wraps a GET handler with session auth.
func (s *Server) page(h func(http.ResponseWriter, *http.Request, view)) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.authed(w, r)
		if !ok {
			return
		}
		h(w, r, v)
	})
}

// action wraps a POST handler with session auth + CSRF + write mode; errors
// render on the referring page via a redirect with a flash-less minimal
// approach (error page).
func (s *Server) action(h func(http.ResponseWriter, *http.Request, view) error) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		v, ok := s.authed(w, r)
		if !ok {
			return
		}
		if !s.write {
			http.Error(w, "server is read-only", http.StatusForbidden)
			return
		}
		if subtle.ConstantTimeCompare([]byte(r.FormValue("csrf")), []byte(v.CSRF)) != 1 || v.CSRF == "" {
			http.Error(w, "invalid csrf token", http.StatusForbidden)
			return
		}
		if err := h(w, r, v); err != nil {
			s.log.Warn("console action failed", "path", r.URL.Path, "err", err)
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	})
}

// render draws a page template.
func (s *Server) render(w http.ResponseWriter, name string, data map[string]any, v view) {
	data["User"] = v.User
	data["CSRF"] = v.CSRF
	if _, ok := data["Error"]; !ok {
		data["Error"] = ""
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl[name].ExecuteTemplate(w, "layout", data); err != nil {
		s.log.Error("template render failed", "page", name, "err", err)
	}
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_ = s.tmpl["login"].ExecuteTemplate(w, "login", map[string]any{"SSO": s.sessions != nil})
}

// DevSessions is a loopback-only development stand-in for the oidc adapter:
// one synthetic owner, fixed CSRF. main guards it behind --dev-auth plus a
// loopback listen address.
type DevSessions struct{}

// SessionUser implements Sessions.
func (DevSessions) SessionUser(*http.Request) (identity.User, string, bool) {
	return identity.User{Subject: "dev", Name: "dev (no auth)", Email: "dev@localhost",
		Service: true}, "dev-csrf", true
}
