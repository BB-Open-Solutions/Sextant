package web

import (
	"bytes"
	"context"
	"crypto/subtle"
	"net/http"
	"net/url"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// view is the per-request context every page gets.
type view struct {
	User   identity.User
	CSRF   string
	L      Localizer
	Unread int // unread notifications, for the header bell badge
	rv     identity.Resolver
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
	// Record the login for the notifier's address book (subject -> e-mail, and
	// group membership for audience mail). Off the request path and best-effort:
	// a directory write must never slow or fail a page load.
	if s.svc.Users != nil {
		go func(u identity.User) {
			// #nosec G118 - deliberate detached context: this best-effort address-book write must outlive the request, so it must not be canceled when the page returns.
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			// Best-effort, but not silent. This table is what
			// EmailsForAudience resolves an approver group against, so a write
			// that keeps failing means somebody simply never receives the mail
			// saying a change is waiting on them - and there is no symptom
			// anywhere that points at this line.
			if err := s.svc.Users.RecordUser(ctx, app.DefaultTenant, u.Subject, u.Email, u.Name, u.Groups); err != nil {
				s.log.Warn("address book: login not recorded; this user may not receive notification mail",
					"subject", u.Subject, "err", err)
			}
		}(u)
	}
	v := view{User: u, CSRF: csrf, L: l, rv: rv}
	// Bell badge: best-effort unread count. A store error leaves the badge at
	// zero rather than blocking the page - notifications are never load-bearing.
	if s.svc.Notify != nil {
		if n, err := s.svc.Notify.Unread(r.Context(), u.Subject, u.Groups); err == nil {
			v.Unread = n
		}
	}
	return v, true
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
			status, msg, detail := classifyActionError(err)
			s.render(w, "error", map[string]any{
				"Title": "Error", "Message": msg, "Detail": detail,
				"Back":     backLink(r),
				"__status": status,
			}, v)
			return
		}
	})
}

// backLink is where the error page's "go back" returns to: the page the
// failed action was submitted from. Only a same-host referer's own path is
// echoed (never a foreign or absolute URL - no open redirect); without one,
// the dashboard.
func backLink(r *http.Request) string {
	ref, err := url.Parse(r.Referer())
	if err != nil || ref.Host != r.Host || !strings.HasPrefix(ref.Path, "/") {
		return "/"
	}
	if ref.RawQuery != "" {
		return ref.Path + "?" + ref.RawQuery
	}
	return ref.Path
}

// render draws a page template.
func (s *Server) render(w http.ResponseWriter, name string, data map[string]any, v view) {
	data["User"] = v.User
	data["CSRF"] = v.CSRF
	data["L"] = v.L
	data["Unread"] = v.Unread
	data["HasNotify"] = s.svc.Notify != nil
	// The organisation IS the scope tree's root; templates show its name
	// wherever a generic "root"/"organisation" would otherwise appear.
	data["OrgName"] = s.orgName
	// Org-wide pages (changes, rollout) refuse scoped viewers; hide the
	// links instead of offering a door that only opens with a 403.
	data["CanOrgView"] = v.canView("org")
	// Same rule for the footer's status link: with a separate metrics
	// listener /status is not on this port at all, so the link 404s for
	// everybody. Being logged in does not help - the split is by port.
	data["HasStatusPage"] = s.statusOnMain
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
	// Render to a buffer first: executing straight into the ResponseWriter
	// turns a template error into a 200 with silently truncated HTML (the
	// status is committed by the first byte). A page-sized copy is nothing
	// next to shipping a broken page as success - that hid a real render
	// bug on the device page for weeks.
	var buf bytes.Buffer
	// A page whose template was never registered is a programming mistake, but
	// it must not take the process down with a nil dereference: the panic
	// looks like a runtime fault and sends whoever debugs it hunting through
	// the handler instead of the one-line list it is actually missing from.
	t := s.tmpl[name]
	if t == nil {
		s.log.Error("no such template", "page", name)
		http.Error(w, "this page is not available", http.StatusInternalServerError)
		return
	}
	if err := t.ExecuteTemplate(&buf, "layout", data); err != nil {
		s.log.Error("template render failed", "page", name, "err", err)
		http.Error(w, "page failed to render; this is a bug - check the console logs", http.StatusInternalServerError)
		return
	}
	// An error page renders a themed body but must keep its 4xx status; set it
	// after the headers, before the body.
	if st, ok := data["__status"].(int); ok {
		w.WriteHeader(st)
	}
	_, _ = buf.WriteTo(w)
}

func (s *Server) handleLoginPage(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	// The language has to travel even here (WCAG 3.1.1). This page used to
	// hardcode lang="en", so a Dutch login screen was announced with English
	// phonetics - and it is the first page every user meets, including the
	// ones who most need the announcement to be right.
	locale := s.defaultLocale
	if locale == "" {
		locale = "en"
	}
	_ = s.tmpl["login"].ExecuteTemplate(w, "login", map[string]any{
		"SSO": s.sessions != nil, "Locale": locale})
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
