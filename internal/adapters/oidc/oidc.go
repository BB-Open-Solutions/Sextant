// Package oidc authenticates console users against one OIDC provider
// (Keycloak, Zitadel, Entra, Authentik) with Authorization Code + PKCE and
// encrypted-cookie sessions. Ported from the proven PoC authenticator.
// Sessions carry identity claims only; authorization is re-derived from the
// current access configuration on every request (domain/identity).
package oidc

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	gooidc "github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// Config configures the authenticator.
type Config struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	// GroupsClaim names the ID-token claim carrying groups. Default "groups".
	GroupsClaim string
	// SessionKey encrypts cookies; exactly 32 bytes.
	SessionKey []byte
	// Secure sets the Secure cookie flag (on behind TLS).
	Secure bool
	// SessionTTL bounds a session. Default 8h.
	SessionTTL time.Duration
	// LandingPath is where a successful login redirects. Default "/".
	LandingPath string
	// GraphURL overrides the Microsoft Graph membership endpoint used for
	// the Entra groups-overage fallback (tests, sovereign clouds).
	GraphURL string
	// Authorize gates login completion: a user who cannot view anything is
	// rejected at the door. Wired to identity.Resolver.CanViewAnything.
	Authorize func(identity.User) bool
	// Logger receives internal detail that must never reach the browser
	// (e.g. the raw Microsoft Graph error body on the groups-overage
	// fallback). Defaults to slog.Default() when nil.
	Logger *slog.Logger
}

// Authenticator implements the OIDC login flow and session middleware.
type Authenticator struct {
	verifier    *gooidc.IDTokenVerifier
	oauth       oauth2.Config
	groupsClaim string
	sess        *secureCookie
	flow        *secureCookie
	ttl         time.Duration
	landing     string
	graphURL    string
	authorize   func(identity.User) bool
	log         *slog.Logger
}

type sessionData struct {
	Subject string   `json:"s"`
	Name    string   `json:"n"`
	Email   string   `json:"e"`
	Groups  []string `json:"g"`
	CSRF    string   `json:"c"`
	Exp     int64    `json:"x"`
}

type flowData struct {
	State    string `json:"s"`
	Nonce    string `json:"n"`
	Verifier string `json:"v"`
	Exp      int64  `json:"x"`
}

// New discovers the provider and builds the authenticator.
func New(ctx context.Context, c Config) (*Authenticator, error) {
	prov, err := gooidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery (%s): %w", c.Issuer, err)
	}
	scopes := c.Scopes
	if len(scopes) == 0 {
		// Groups usually arrive via an IdP claim mapper, not a scope.
		scopes = []string{gooidc.ScopeOpenID, "profile", "email"}
	}
	ttl := c.SessionTTL
	if ttl <= 0 {
		ttl = 8 * time.Hour
	}
	sess, err := newSecureCookie("sextant_session", c.SessionKey, c.Secure, int(ttl.Seconds()))
	if err != nil {
		return nil, err
	}
	flow, err := newSecureCookie("sextant_flow", c.SessionKey, c.Secure, 600)
	if err != nil {
		return nil, err
	}
	gc := c.GroupsClaim
	if gc == "" {
		gc = "groups"
	}
	landing := c.LandingPath
	if landing == "" {
		landing = "/"
	}
	authorize := c.Authorize
	if authorize == nil {
		authorize = func(identity.User) bool { return true }
	}
	log := c.Logger
	if log == nil {
		log = slog.Default()
	}
	return &Authenticator{
		verifier: prov.Verifier(&gooidc.Config{ClientID: c.ClientID}),
		oauth: oauth2.Config{
			ClientID: c.ClientID, ClientSecret: c.ClientSecret,
			Endpoint: prov.Endpoint(), RedirectURL: c.RedirectURL, Scopes: scopes,
		},
		groupsClaim: gc,
		sess:        sess,
		flow:        flow,
		ttl:         ttl,
		landing:     landing,
		graphURL:    c.GraphURL,
		authorize:   authorize,
		log:         log,
	}, nil
}

// Routes registers the login flow. The /login page itself belongs to the
// console; /login/start begins the IdP redirect.
func (a *Authenticator) Routes(mux *http.ServeMux) {
	mux.HandleFunc("GET /login/start", a.Login)
	mux.HandleFunc("GET /callback", a.Callback)
	mux.HandleFunc("POST /logout", a.Logout)
}

// Login starts the Authorization Code + PKCE flow.
func (a *Authenticator) Login(w http.ResponseWriter, r *http.Request) {
	state, nonce, verifier := randString(), randString(), oauth2.GenerateVerifier()
	fd := flowData{State: state, Nonce: nonce, Verifier: verifier,
		Exp: time.Now().Add(10 * time.Minute).Unix()}
	if err := a.flow.set(w, fd); err != nil {
		http.Error(w, "login init failed", http.StatusInternalServerError)
		return
	}
	url := a.oauth.AuthCodeURL(state, gooidc.Nonce(nonce), oauth2.S256ChallengeOption(verifier))
	http.Redirect(w, r, url, http.StatusFound)
}

// Callback completes the flow: verify state, exchange the code (PKCE),
// verify the ID token and nonce, authorize, then create the session.
func (a *Authenticator) Callback(w http.ResponseWriter, r *http.Request) {
	var fl flowData
	if err := a.flow.get(r, &fl); err != nil || time.Now().Unix() > fl.Exp {
		http.Error(w, "login flow expired, try again", http.StatusBadRequest)
		return
	}
	a.flow.clear(w)
	if q := r.URL.Query(); q.Get("error") != "" {
		http.Error(w, "idp error: "+q.Get("error"), http.StatusBadRequest)
		return
	}
	if subtle.ConstantTimeCompare([]byte(r.URL.Query().Get("state")), []byte(fl.State)) != 1 {
		http.Error(w, "bad state", http.StatusBadRequest)
		return
	}
	tok, err := a.oauth.Exchange(r.Context(), r.URL.Query().Get("code"), oauth2.VerifierOption(fl.Verifier))
	if err != nil {
		http.Error(w, "token exchange failed", http.StatusUnauthorized)
		return
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		http.Error(w, "no id_token in response", http.StatusUnauthorized)
		return
	}
	idt, err := a.verifier.Verify(r.Context(), rawID)
	if err != nil {
		http.Error(w, "id_token verification failed", http.StatusUnauthorized)
		return
	}
	if subtle.ConstantTimeCompare([]byte(idt.Nonce), []byte(fl.Nonce)) != 1 {
		http.Error(w, "nonce mismatch", http.StatusUnauthorized)
		return
	}
	var claims map[string]any
	if err := idt.Claims(&claims); err != nil {
		http.Error(w, "cannot read claims", http.StatusInternalServerError)
		return
	}
	groups := groupsFromClaims(claims, a.groupsClaim)
	// Entra groups overage: >150 groups means no groups claim, only a
	// pointer to Graph. Fetch the real membership or large-tenant RBAC
	// silently sees nothing (the failure mode this fallback exists for).
	if len(groups) == 0 && hasGroupsOverage(claims) {
		fetched, err := fetchGroupsFromGraph(r.Context(), nil, a.graphURL, tok.AccessToken)
		if err != nil {
			// The Graph error can carry upstream response detail
			// (endpoint, tenant/request identifiers); log it server-side
			// only and return a generic message to the browser, which is
			// IdP-authenticated but not yet authorized at this point.
			a.log.Error("entra groups overage: graph membership lookup failed", "err", err)
			http.Error(w, "group membership lookup failed", http.StatusBadGateway)
			return
		}
		groups = fetched
	}
	u := identity.User{
		Subject: idt.Subject,
		Name:    firstStr(claims, "name", "preferred_username", "email"),
		Email:   str(claims["email"]),
		Groups:  groups,
	}
	if !a.authorize(u) {
		http.Error(w, "not authorized: no role grants console access to your account", http.StatusForbidden)
		return
	}
	sd := sessionData{Subject: u.Subject, Name: u.Name, Email: u.Email,
		Groups: u.Groups, CSRF: randString(), Exp: time.Now().Add(a.ttl).Unix()}
	if err := a.sess.set(w, sd); err != nil {
		http.Error(w, "session error", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, a.landing, http.StatusSeeOther)
}

// Logout clears the session. It is state-changing, so - like every other
// session mutation in the console - it requires the caller to echo the
// session's own CSRF token; the layout template already sends it as a
// hidden form field. POST /logout is mounted directly (not through the web
// package's action wrapper), so without this check a cross-site
// auto-submitting form could force a visitor's session to be cleared with no
// consent. A request with no valid session simply logs out (nothing to
// protect), so an already-expired/missing cookie is not itself an error.
func (a *Authenticator) Logout(w http.ResponseWriter, r *http.Request) {
	var sd sessionData
	if err := a.sess.get(r, &sd); err == nil && time.Now().Unix() <= sd.Exp {
		csrf := r.FormValue("csrf")
		if csrf == "" {
			csrf = r.Header.Get("X-CSRF-Token")
		}
		if subtle.ConstantTimeCompare([]byte(csrf), []byte(sd.CSRF)) != 1 {
			http.Error(w, "missing or invalid csrf token", http.StatusForbidden)
			return
		}
	}
	a.sess.clear(w)
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// SessionUser returns the authenticated user and CSRF token for a request,
// or false when unauthenticated/expired. Verdicts (roles) are NOT stored in
// the session; the caller derives them from current config.
func (a *Authenticator) SessionUser(r *http.Request) (identity.User, string, bool) {
	var sd sessionData
	if err := a.sess.get(r, &sd); err != nil || time.Now().Unix() > sd.Exp {
		return identity.User{}, "", false
	}
	return identity.User{Subject: sd.Subject, Name: sd.Name, Email: sd.Email, Groups: sd.Groups}, sd.CSRF, true
}

// randStringBytes is the byte length every randString call uses: enough
// entropy for OAuth state/nonce and the session CSRF token.
const randStringBytes = 24

// randString returns randStringBytes cryptographically random bytes,
// base64url-encoded. It feeds the OAuth state and nonce and the session CSRF
// token - all security-critical, so silently returning an all-zero (thus
// predictable) value on RNG failure is unacceptable. crypto/rand.Read only
// errors when the OS CSPRNG itself is broken, which is unrecoverable;
// panicking is preferable to issuing a guessable token, and mw.Recover
// (wrapped around every handler) turns it into a 500 rather than crashing
// the process.
func randString() string {
	b := make([]byte, randStringBytes)
	if _, err := rand.Read(b); err != nil {
		panic("oidc: crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}
