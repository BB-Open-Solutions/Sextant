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

// TokenAuthenticator resolves a bearer secret to a principal plus an
// optional ceiling role (ADR 0008). Implemented by app.TokenService.
type TokenAuthenticator interface {
	Authenticate(ctx context.Context, secret string) (identity.User, identity.Role, bool)
}

// Authz derives per-scope roles from the current fleet document. Baselines
// are the server-configured org-wide groups.
type Authz struct {
	Sessions       SessionSource
	Tokens         TokenAuthenticator
	BaselineViewer []string
	BaselineEditor []string
	BaselineOwner  []string
}

type principalKey struct{}

type principal struct {
	user identity.User
	csrf string // set only for session principals
	// bearer marks a token/break-glass principal: no ambient credential,
	// so CSRF does not apply (CSRF defends cookies, not bearer tokens).
	bearer bool
	// ceiling narrows a token below its owner (identity.None = no ceiling).
	ceiling identity.Role
	hasCap  bool
}

// authenticate resolves the request principal, in order: the break-glass
// static token (owner-everywhere service), then a personal/service token
// (acts as its owner, one authorization path), then a browser session.
func (a *API) authenticate(r *http.Request) (principal, bool) {
	got := bearerToken(r)

	// Break-glass static token: owner everywhere. Explicitly a fallback
	// until scoped tokens fully replace it (ADR 0008).
	if a.token != "" && got != "" &&
		subtle.ConstantTimeCompare([]byte(got), []byte(a.token)) == 1 {
		return principal{bearer: true, user: identity.User{
			Subject: "svc:api", Name: "api", Service: true,
		}}, true
	}

	// Scoped token: resolves to its owner's identity, judged by the same
	// resolver. A ceiling can only reduce the resulting rights.
	if a.authz.Tokens != nil && got != "" {
		if u, ceiling, ok := a.authz.Tokens.Authenticate(r.Context(), got); ok {
			return principal{bearer: true, user: u, ceiling: ceiling, hasCap: ceiling != identity.None}, true
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
// deriving from the live fleet document (never from stored verdicts). A
// token ceiling clamps the effective role below the owner's - it can only
// reduce, never widen.
func (a *API) require(r *http.Request, ref string, role identity.Role) error {
	p := principalFrom(r.Context())
	rv := a.cfg.Fleet().IdentityResolver(
		a.authz.BaselineViewer, a.authz.BaselineEditor, a.authz.BaselineOwner)
	got := rv.RoleAt(p.user, ref)
	if p.hasCap && p.ceiling < got {
		got = p.ceiling // ceiling narrows, never widens
	}
	if !got.Meets(role) {
		return &forbidden{fmt.Errorf("requires %s at %s (you hold %s)", role, ref, got)}
	}
	return nil
}

// verifyCSRF guards session-authenticated mutations: the browser must echo
// the session CSRF token in a header. Service principals (bearer token, no
// ambient credential) are exempt.
func (p principal) verifyCSRF(r *http.Request) bool {
	if p.bearer {
		return true // bearer tokens carry no ambient credential
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
		return ports.Author{Subject: p.user.Subject, Name: "sextant-api", Email: "api@sextant"}
	}
	email := p.user.Email
	if email == "" {
		email = p.user.Subject + "@idp"
	}
	name := p.user.Name
	if name == "" {
		name = p.user.Subject
	}
	return ports.Author{Subject: p.user.Subject, Name: name, Email: email}
}
