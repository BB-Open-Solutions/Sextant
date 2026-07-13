package oidc

// login_callback_test.go drives Login and Callback end to end against a
// fake OIDC provider (discovery doc + JWKS + token endpoint, RSA-signed ID
// tokens) built with net/http/httptest. These are the security-critical
// handlers: everything else in this package is a pure helper already
// covered elsewhere. No real IdP is reachable in CI, so the fake stands in
// for Keycloak/Zitadel/Entra without weakening what's actually asserted -
// signature verification, issuer/audience checks and nonce binding all run
// for real via coreos/go-oidc.

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"
	"time"

	jose "github.com/go-jose/go-jose/v4"
)

// fakeIDP is a minimal OIDC provider: discovery, JWKS and a token endpoint
// that mints an RSA-signed ID token from whatever claims the test last
// configured. The authorize endpoint is deliberately absent - Login only
// builds its URL, it never dereferences it, so the flow under test never
// needs a browser to actually hit it.
type fakeIDP struct {
	URL      string
	clientID string
	key      *rsa.PrivateKey
	kid      string

	mu     sync.Mutex
	claims map[string]any
}

func newFakeIDP(t *testing.T, clientID string) *fakeIDP {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	idp := &fakeIDP{clientID: clientID, key: key, kid: "test-key-1"}

	var srvURL string
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer":                                srvURL,
			"authorization_endpoint":                srvURL + "/authorize",
			"token_endpoint":                        srvURL + "/token",
			"jwks_uri":                              srvURL + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{
			{Key: &idp.key.PublicKey, KeyID: idp.kid, Algorithm: "RS256", Use: "sig"},
		}}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(set)
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, _ *http.Request) {
		idp.mu.Lock()
		claims := idp.claims
		idp.mu.Unlock()
		if claims == nil {
			http.Error(w, "fake idp: no id token claims configured for this exchange", http.StatusInternalServerError)
			return
		}
		idTok, err := idp.sign(claims)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at-test-token",
			"token_type":   "Bearer",
			"expires_in":   3600,
			"id_token":     idTok,
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	srvURL = srv.URL
	idp.URL = srv.URL
	return idp
}

func (f *fakeIDP) sign(claims map[string]any) (string, error) {
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	opts := (&jose.SignerOptions{}).WithHeader(jose.HeaderKey("kid"), f.kid)
	signer, err := jose.NewSigner(jose.SigningKey{Algorithm: jose.RS256, Key: f.key}, opts)
	if err != nil {
		return "", err
	}
	jws, err := signer.Sign(payload)
	if err != nil {
		return "", err
	}
	return jws.CompactSerialize()
}

// setIDTokenClaims configures what the next /token call mints. Nonce is
// deliberately NOT part of the defaults: tests exercising nonce validation
// pass it explicitly, and simply not overriding it reproduces a provider
// that dropped the nonce claim from the token entirely.
func (f *fakeIDP) setIDTokenClaims(overrides map[string]any) {
	now := time.Now()
	claims := map[string]any{
		"iss": f.URL,
		"aud": f.clientID,
		"sub": "user-1",
		"iat": now.Unix(),
		"exp": now.Add(time.Hour).Unix(),
	}
	for k, v := range overrides {
		claims[k] = v
	}
	f.mu.Lock()
	f.claims = claims
	f.mu.Unlock()
}

func newTestAuthenticator(t *testing.T, idp *fakeIDP, clientID string) *Authenticator {
	t.Helper()
	a, err := New(context.Background(), Config{
		Issuer:       idp.URL,
		ClientID:     clientID,
		ClientSecret: "test-secret",
		RedirectURL:  "http://console.example/callback",
		SessionKey:   key32(),
		Secure:       false,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return a
}

// login drives Login and returns the recorded response together with the
// state and nonce the redirect exposed, so callers can build a matching (or
// deliberately mismatching) callback request.
func login(t *testing.T, a *Authenticator) (rec *httptest.ResponseRecorder, state, nonce string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/login/start", nil)
	rec = httptest.NewRecorder()
	a.Login(rec, req)
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect location %q: %v", rec.Header().Get("Location"), err)
	}
	q := loc.Query()
	return rec, q.Get("state"), q.Get("nonce")
}

func sessionCookie(cookies []*http.Cookie) *http.Cookie {
	for _, c := range cookies {
		if c.Name == "sextant_session" {
			return c
		}
	}
	return nil
}

func TestLoginRedirectsWithStateAndNonce(t *testing.T) {
	idp := newFakeIDP(t, "test-client")
	a := newTestAuthenticator(t, idp, "test-client")

	req := httptest.NewRequest(http.MethodGet, "/login/start", nil)
	rec := httptest.NewRecorder()
	a.Login(rec, req)

	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusFound)
	}
	loc, err := url.Parse(rec.Header().Get("Location"))
	if err != nil {
		t.Fatalf("bad redirect location: %v", err)
	}
	if got := loc.Scheme + "://" + loc.Host + loc.Path; got != idp.URL+"/authorize" {
		t.Fatalf("redirect target = %s, want %s/authorize", got, idp.URL)
	}
	q := loc.Query()
	state, nonce := q.Get("state"), q.Get("nonce")
	if state == "" || nonce == "" {
		t.Fatalf("state/nonce missing from authorize URL: %s", loc)
	}
	if q.Get("code_challenge") == "" || q.Get("code_challenge_method") != "S256" {
		t.Fatalf("PKCE challenge missing from authorize URL: %s", loc)
	}

	cookies := rec.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != "sextant_flow" {
		t.Fatalf("flow cookie = %+v", cookies)
	}
	// The cookie must carry the very same state/nonce the redirect exposed -
	// Callback trusts the cookie, not the query string, for what "correct"
	// means.
	cr := httptest.NewRequest(http.MethodGet, "/", nil)
	cr.AddCookie(cookies[0])
	var fd flowData
	if err := a.flow.get(cr, &fd); err != nil {
		t.Fatalf("flow cookie unreadable: %v", err)
	}
	if fd.State != state || fd.Nonce != nonce {
		t.Fatalf("cookie state/nonce = %q/%q, want %q/%q", fd.State, fd.Nonce, state, nonce)
	}
}

func TestCallbackEstablishesSession(t *testing.T) {
	idp := newFakeIDP(t, "test-client")
	a := newTestAuthenticator(t, idp, "test-client")

	rec, state, nonce := login(t, a)
	idp.setIDTokenClaims(map[string]any{
		"nonce":  nonce,
		"sub":    "user-1",
		"email":  "ada@example.com",
		"name":   "Ada Lovelace",
		"groups": []string{"editors"},
	})

	cbReq := httptest.NewRequest(http.MethodGet, "/callback?state="+state+"&code=test-code", nil)
	for _, c := range rec.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	a.Callback(cbRec, cbReq)

	if cbRec.Code != http.StatusSeeOther {
		t.Fatalf("status = %d body=%s, want %d", cbRec.Code, cbRec.Body.String(), http.StatusSeeOther)
	}
	sc := sessionCookie(cbRec.Result().Cookies())
	if sc == nil || sc.Value == "" {
		t.Fatal("no session cookie set on success")
	}

	sr := httptest.NewRequest(http.MethodGet, "/", nil)
	sr.AddCookie(sc)
	u, csrf, ok := a.SessionUser(sr)
	if !ok {
		t.Fatal("session cookie did not decode to an active session")
	}
	if u.Subject != "user-1" || u.Email != "ada@example.com" || u.Name != "Ada Lovelace" {
		t.Fatalf("session user = %+v", u)
	}
	if csrf == "" {
		t.Fatal("no CSRF token in session")
	}
}

func TestCallbackRejectsMismatchedState(t *testing.T) {
	idp := newFakeIDP(t, "test-client")
	a := newTestAuthenticator(t, idp, "test-client")

	rec, _, nonce := login(t, a)
	// A token would be mintable (valid nonce) but must never be reached:
	// the state check happens first.
	idp.setIDTokenClaims(map[string]any{"nonce": nonce})

	cbReq := httptest.NewRequest(http.MethodGet, "/callback?state=attacker-supplied-state&code=test-code", nil)
	for _, c := range rec.Result().Cookies() {
		cbReq.AddCookie(c)
	}
	cbRec := httptest.NewRecorder()
	a.Callback(cbRec, cbReq)

	if cbRec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d body=%s, want %d", cbRec.Code, cbRec.Body.String(), http.StatusBadRequest)
	}
	if sc := sessionCookie(cbRec.Result().Cookies()); sc != nil {
		t.Fatal("session cookie set despite mismatched state")
	}
}

func TestCallbackRejectsBadNonce(t *testing.T) {
	cases := []struct {
		name     string
		override map[string]any // no "nonce" key at all means the claim is absent
	}{
		{"wrong nonce", map[string]any{"nonce": "not-the-flow-nonce"}},
		{"absent nonce", map[string]any{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			idp := newFakeIDP(t, "test-client")
			a := newTestAuthenticator(t, idp, "test-client")

			rec, state, _ := login(t, a)
			idp.setIDTokenClaims(tc.override)

			cbReq := httptest.NewRequest(http.MethodGet, "/callback?state="+state+"&code=test-code", nil)
			for _, c := range rec.Result().Cookies() {
				cbReq.AddCookie(c)
			}
			cbRec := httptest.NewRecorder()
			a.Callback(cbRec, cbReq)

			if cbRec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d body=%s, want %d", cbRec.Code, cbRec.Body.String(), http.StatusUnauthorized)
			}
			if sc := sessionCookie(cbRec.Result().Cookies()); sc != nil {
				t.Fatal("session cookie set despite nonce mismatch")
			}
		})
	}
}
