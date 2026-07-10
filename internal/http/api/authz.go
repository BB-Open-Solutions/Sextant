package api

import (
	"context"
	"crypto/subtle"
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// SessionSource lets humans use the API with a browser session (the UI and
// htmx calls). Implemented by the oidc adapter; nil means token-only.
type SessionSource interface {
	SessionUser(r *http.Request) (identity.User, string, bool)
}

// Authz derives per-scope roles from the current fleet document. Baselines
// are the server-configured org-wide groups.
type Authz struct {
	Sessions       SessionSource
	BaselineViewer []string
	BaselineEditor []string
	BaselineOwner  []string
}

type principalKey struct{}

type principal struct {
	user identity.User
	csrf string // empty for service principals (no ambient cookie, no CSRF)
}

// authenticate resolves the request principal: a valid bearer token yields
// the service principal; otherwise a session cookie yields the human user.
func (a *API) authenticate(r *http.Request) (principal, bool) {
	if a.token != "" {
		got := bearerToken(r)
		if got != "" && subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1 {
			return principal{user: identity.User{
				Subject: "svc:api", Name: "api", Service: true,
			}}, true
		}
	}
	if a.authz.Sessions != nil {
		if u, csrf, ok := a.authz.Sessions.SessionUser(r); ok {
			return principal{user: u, csrf: csrf}, true
		}
	}
	return principal{}, false
}

// withPrincipal stores the principal for handlers.
func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) principal {
	p, _ := ctx.Value(principalKey{}).(principal)
	return p
}

// require asserts the principal holds at least role at the scope ref,
// deriving from the live fleet document (never from stored verdicts).
func (a *API) require(r *http.Request, ref string, role identity.Role) error {
	p := principalFrom(r.Context())
	rv := a.cfg.Fleet().IdentityResolver(
		a.authz.BaselineViewer, a.authz.BaselineEditor, a.authz.BaselineOwner)
	if got := rv.RoleAt(p.user, ref); !got.Meets(role) {
		return &forbidden{fmt.Errorf("requires %s at %s (you hold %s)", role, ref, got)}
	}
	return nil
}

// verifyCSRF guards session-authenticated mutations: the browser must echo
// the session CSRF token in a header. Service principals (bearer token, no
// ambient credential) are exempt.
func (p principal) verifyCSRF(r *http.Request) bool {
	if p.user.Service {
		return true
	}
	got := r.Header.Get("X-CSRF-Token")
	return p.csrf != "" && subtle.ConstantTimeCompare([]byte(p.csrf), []byte(got)) == 1
}

// forbidden maps to 403 with the reason.
type forbidden struct{ err error }

func (e *forbidden) Error() string { return e.err.Error() }
func (e *forbidden) Unwrap() error { return e.err }

// author derives commit attribution from the request principal.
func author(r *http.Request) ports.Author {
	p := principalFrom(r.Context())
	if p.user.Subject == "" {
		return ports.Author{Name: "sextant", Email: "sextant@localhost"}
	}
	if p.user.Service {
		return ports.Author{Name: "sextant-api", Email: "api@sextant"}
	}
	email := p.user.Email
	if email == "" {
		email = p.user.Subject + "@idp"
	}
	name := p.user.Name
	if name == "" {
		name = p.user.Subject
	}
	return ports.Author{Name: name, Email: email}
}
