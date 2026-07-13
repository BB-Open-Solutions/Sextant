// Package web is the human console: server-rendered pages over the same app
// services the JSON API uses. Handlers are thin - parse, one service call,
// render - and every mutation carries a CSRF token and per-scope RBAC.
package web

import (
	"crypto/sha256"
	"crypto/subtle"
	"embed"
	"encoding/hex"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

//go:embed templates/*.html static/*
var assets embed.FS

// diffLine is one classified line of a unified diff for the change viewer.
type diffLine struct {
	Kind string // add | del | hunk | meta | ctx
	Text string
}

// Sessions provides the authenticated user for a request (the oidc
// adapter, or a dev stub on loopback).
type Sessions interface {
	SessionUser(r *http.Request) (identity.User, string, bool)
}

// Services are the app services the console renders.
type Services struct {
	Config       *app.ConfigService
	Changes      *app.ChangeService
	Rollouts     *app.RolloutService
	Inventory    *app.InventoryService
	Tokens       *app.TokenService
	Prefs        ports.PrefsStore
	DevCreds     *app.DeviceCredentials
	Directory    ports.Directory
	Evidence     *app.EvidenceService
	Discovery    *app.DiscoveryService
	Imaging      *app.ImagingService
	StationCreds *app.StationCredentials
}

// Server renders the console.
type Server struct {
	svc      Services
	sessions Sessions
	tmpl     map[string]*template.Template
	log      *slog.Logger
	write    bool

	baseViewer, baseEditor, baseOwner []string

	// Organisation presentation defaults; user preferences override.
	defaultLocale, defaultTZ string
}

// SetDefaults configures the organisation's presentation defaults
// (locale and IANA timezone) applied when a user has no preference.
func (s *Server) SetDefaults(locale, tz string) {
	if locale != "" {
		s.defaultLocale = locale
	}
	if tz != "" {
		s.defaultTZ = tz
	}
}

// New builds the console server. Baselines mirror the API's org-wide role
// groups.
func New(svc Services, sessions Sessions, write bool,
	baseViewer, baseEditor, baseOwner []string, log *slog.Logger) (*Server, error) {
	// funcs: small template helpers. `list` lets a template iterate a
	// literal set (e.g. the CLI toolbelt commands) without a data field.
	funcs := template.FuncMap{
		"list":      func(items ...any) []any { return items },
		"hasPrefix": strings.HasPrefix,
		// initial is the uppercase first letter of a name, for the avatar
		// fallback when no profile photo is available.
		"initial": func(s string) string {
			for _, r := range strings.TrimSpace(s) {
				return strings.ToUpper(string(r))
			}
			return "?"
		},
		// contains reports whether a string slice holds v (e.g. is a
		// setting key in a policy's enforced/locked list).
		"contains": func(list []string, v string) bool {
			for _, s := range list {
				if s == v {
					return true
				}
			}
			return false
		},
		// difflines classifies a unified diff into coloured lines for the
		// change viewer (add/del/hunk/meta/context), CSP-safe via classes.
		"difflines": func(diff string) []diffLine {
			lines := strings.Split(diff, "\n")
			out := make([]diffLine, 0, len(lines))
			for _, ln := range lines {
				kind := "ctx"
				switch {
				case strings.HasPrefix(ln, "+++"), strings.HasPrefix(ln, "---"),
					strings.HasPrefix(ln, "diff "), strings.HasPrefix(ln, "index "):
					kind = "meta"
				case strings.HasPrefix(ln, "@@"):
					kind = "hunk"
				case strings.HasPrefix(ln, "+"):
					kind = "add"
				case strings.HasPrefix(ln, "-"):
					kind = "del"
				}
				out = append(out, diffLine{Kind: kind, Text: ln})
			}
			return out
		},
		// initials renders up to two uppercase initials from a display
		// name for the audit avatar (a display transform, not new data).
		"initials": func(name string) string {
			parts := strings.Fields(name)
			var b strings.Builder
			for _, p := range parts {
				if b.Len() >= 2 {
					break
				}
				b.WriteString(strings.ToUpper(p[:1]))
			}
			if b.Len() == 0 {
				return "?"
			}
			return b.String()
		},
		// indent maps a group-tree depth to a static padding class
		// (gd-0..gd-6, clamped). A class avoids inline style=, which
		// the CSP forbids.
		"indent": func(depth int) string {
			if depth < 0 {
				depth = 0
			}
			if depth > 6 {
				depth = 6
			}
			return fmt.Sprintf("gd-%d", depth)
		},
	}
	pages := []string{"overview", "devices", "device", "groups", "settings", "policies", "changes", "diff", "rollout", "access", "audit", "profile", "station", "secrets", "pipeline", "service_accounts", "enroll", "integrations", "overlays"}
	tmpl := make(map[string]*template.Template, len(pages)+1)
	for _, p := range pages {
		t, err := template.New("layout.html").Funcs(funcs).ParseFS(assets, "templates/layout.html", "templates/"+p+".html")
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
		baseViewer: baseViewer, baseEditor: baseEditor, baseOwner: baseOwner,
		defaultLocale: "en", defaultTZ: "UTC"}, nil
}

// staticHandler serves the embedded assets with a content-hash ETag and
// revalidation. Assets live under a stable filename (e.g. the icon font), so
// without this a browser caches them indefinitely and never sees an update -
// a new font subset or stylesheet stays invisible until a manual hard-refresh.
// A per-file ETag lets the browser revalidate cheaply: unchanged files answer
// 304, a changed file is refetched on the next load.
func staticHandler() http.Handler {
	etags := map[string]string{}
	_ = fs.WalkDir(assets, "static", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := assets.ReadFile(p)
		if err != nil {
			return nil
		}
		sum := sha256.Sum256(b)
		etags["/"+p] = `"` + hex.EncodeToString(sum[:16]) + `"`
		return nil
	})
	files := http.FileServerFS(assets)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if et, ok := etags[r.URL.Path]; ok {
			// no-cache = store, but revalidate before use. ServeContent (via
			// FileServerFS) honours If-None-Match against this ETag and
			// returns 304 when the content is unchanged.
			w.Header().Set("ETag", et)
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
}

// Routes registers the console.
func (s *Server) Routes(mux *http.ServeMux) {
	mux.Handle("GET /static/", staticHandler())
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
	get("/groups", s.groupsPage)
	get("/settings", s.settingsPage)
	get("/policies", s.policies)
	get("/changes", s.changesPage)
	get("/changes/{id}/diff", s.diffPage)
	get("/pipeline", s.pipelinePage)
	get("/rollout", s.rolloutPage)
	get("/access", s.accessPage)
	get("/audit", s.auditPage)
	get("/audit/evidence", s.auditEvidence)
	get("/downloads/sxctl", s.downloadCLI)
	get("/downloads/sxctl.sha256", s.cliChecksum)
	get("/station", s.stationPage)
	get("/enroll", s.enrollPage)
	get("/integrations", s.integrationsPage)
	get("/overlays", s.overlaysPage)
	get("/secrets", s.secretsPage)
	get("/service-accounts", s.serviceAccountsPage)
	get("/profile", s.profilePage)

	post("/devices", s.postDeviceEnroll)
	post("/devices/{tag}/settings", s.postDeviceSetting)
	post("/devices/{tag}/posture", s.postDevicePosture)
	post("/devices/{tag}/intent", s.postDeviceIntent)
	post("/devices/{tag}/intent/clear", s.postDeviceIntentClear)
	post("/devices/{tag}/retire", s.postDeviceRetire)
	post("/devices/{tag}/reactivate", s.postDeviceReactivate)
	post("/devices/{tag}/remove", s.postDeviceRemove)
	post("/devices/{tag}/credential", s.postDeviceCredential)
	post("/devices/{tag}/update", s.postDeviceUpdate)
	post("/settings", s.postSetting)
	post("/policies", s.postPolicyPut)
	post("/policies/{id}/delete", s.postPolicyDelete)
	post("/assignments", s.postAssignmentAdd)
	post("/assignments/delete", s.postAssignmentDelete)
	post("/filters", s.postFilterPut)
	post("/filters/{id}/delete", s.postFilterDelete)
	post("/apps", s.postScopeApps)
	post("/groups", s.postGroupAdd)
	post("/groups/{name}/update", s.postGroupUpdate)
	post("/groups/{name}/remove", s.postGroupRemove)
	post("/changes", s.postChange)
	post("/changes/{id}/edits", s.postChangeEdit)
	post("/changes/{id}/submit", s.postChangeSubmit)
	post("/changes/{id}/merge", s.postChangeMerge)
	post("/changes/{id}/abandon", s.postChangeAbandon)
	post("/rollout", s.postRolloutStart)
	post("/rollout/plan", s.postRolloutPlan)
	post("/rollout/tick", s.postRolloutTick)
	post("/rollout/approve", s.postRolloutApprove)
	post("/rollout/cancel", s.postRolloutCancel)
	post("/access/grant", s.postAccessGrant)
	post("/access/revoke", s.postAccessRevoke)
	post("/assurance", s.postAssurance)
	post("/profile/prefs", s.postProfilePrefs)
	post("/profile/tokens", s.postProfileTokenMint)
	post("/profile/tokens/{id}/revoke", s.postProfileTokenRevoke)
	post("/enroll/{station}/batch", s.postEnrollBatch)
	post("/enroll/{station}/image", s.postEnrollImage)
	post("/enroll/{station}/jobs/{mac}/cancel", s.postEnrollJobCancel)
	post("/stations", s.postStationRegister)
	post("/station/{tag}/credential", s.postStationCredential)
	post("/station/{tag}/remove", s.postStationRemove)
	post("/secrets", s.postSecretRegister)
	post("/secrets/{name}/remove", s.postSecretRemove)
	post("/overlays", s.postOverlayWrite)
	post("/overlays/{name}/remove", s.postOverlayRemove)
	post("/service-accounts", s.postServiceAccountMint)
	post("/service-accounts/{id}/revoke", s.postServiceAccountRevoke)
	post("/acceptances", s.postAcceptance)
	post("/acceptances/clear", s.postAcceptanceClear)
}

// view is the per-request context every page gets.
type view struct {
	User identity.User
	CSRF string
	L    Localizer
	rv   identity.Resolver
}

func (v view) roleAt(ref string) identity.Role { return v.rv.RoleAt(v.User, ref) }

// canView is the read-visibility predicate for fleet.VisibleTo: pages render
// only the scopes the user may see (per-scope read-confidentiality).
func (v view) canView(ref string) bool { return v.roleAt(ref).Meets(identity.Viewer) }

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
	// Presentation preferences; a store error falls back to org defaults
	// (a broken prefs table must not lock anyone out).
	var prefs identity.Preferences
	if s.svc.Prefs != nil {
		if p, ok, err := s.svc.Prefs.GetPrefs(r.Context(), app.DefaultTenant, u.Subject); err == nil && ok {
			prefs = p
		}
	}
	l := newLocalizer(prefs, s.defaultLocale, s.defaultTZ)
	return view{User: u, CSRF: csrf, L: l, rv: rv}, true
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
		// Console forms are small; bound the body before ParseForm buffers
		// it, mirroring the API's request caps.
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
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
	data["L"] = v.L
	// Org-wide pages (changes, rollout) refuse scoped viewers; hide the
	// links instead of offering a door that only opens with a 403.
	data["CanOrgView"] = v.canView("org")
	// CanOrgOwn gates owner-only nav entries (service accounts); pages that
	// already set their own CanOrgOwn keep theirs.
	if _, ok := data["CanOrgOwn"]; !ok {
		data["CanOrgOwn"] = v.roleAt("org").Meets(identity.Owner)
	}
	if _, ok := data["Error"]; !ok {
		data["Error"] = ""
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// Authenticated pages can carry one-shot secrets (minted tokens,
	// device credentials); no browser or intermediary may cache them, or
	// the back button re-renders a secret after its cookie is consumed.
	w.Header().Set("Cache-Control", "no-store")
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
