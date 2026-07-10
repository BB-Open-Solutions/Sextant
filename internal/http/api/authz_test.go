package api

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	tokenpkg "code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// fakeSessions injects a fixed user per request via a test header, standing
// in for the oidc adapter.
type fakeSessions struct {
	users map[string]identity.User // key = value of X-Test-User
	csrf  string
}

func (f *fakeSessions) SessionUser(r *http.Request) (identity.User, string, bool) {
	u, ok := f.users[r.Header.Get("X-Test-User")]
	return u, f.csrf, ok
}

// rbacSeed: group tree zaanstad -> frontoffice; device lt-1 in frontoffice;
// access grants fo-editors editor at frontoffice, za-owners owner at
// zaanstad, auditors viewer at org.
const rbacSeed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"zaanstad": {}, "frontoffice": {"parent": "zaanstad"}, "elsewhere": {}},
  "devices": {"lt-1": {"groups": ["frontoffice"], "hardware": "hw"}},
  "access": [
    {"group": "fo-editors", "role": "editor", "scope": "group:frontoffice"},
    {"group": "za-owners", "role": "owner", "scope": "group:zaanstad"},
    {"group": "auditors", "role": "viewer", "scope": "org"}
  ]
}`

func newRBACServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	svc, dir := seededService(t, rbacSeed)
	sessions := &fakeSessions{
		csrf: "test-csrf",
		users: map[string]identity.User{
			"editor":  {Subject: "ed", Name: "Edith Editor", Email: "edith@x", Groups: []string{"fo-editors"}},
			"owner":   {Subject: "ow", Name: "Oscar Owner", Email: "oscar@x", Groups: []string{"za-owners"}},
			"auditor": {Subject: "au", Groups: []string{"auditors"}},
			"nobody":  {Subject: "no", Groups: []string{"unrelated"}},
		},
	}
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{Sessions: sessions}, "", true,
		discardLog()).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv, dir
}

// seededService builds a ConfigService over a temp repo with a given seed
// document, returning the repo dir for git-log assertions.
func seededService(t *testing.T, seedDoc string) (*app.ConfigService, string) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "fleet.json")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

// sessionCall performs a request as a session user.
func sessionCall(t *testing.T, srv *httptest.Server, user, method, path string, body any, csrf string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	req.Header.Set("X-Test-User", user)
	if csrf != "" {
		req.Header.Set("X-CSRF-Token", csrf)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

func TestSessionRBACOnSettings(t *testing.T) {
	srv, dir := newRBACServer(t)
	set := func(user, scope, csrf string) int {
		code, _ := sessionCall(t, srv, user, "POST", "/api/v1/settings",
			map[string]any{"scope": scope, "key": "apps.office", "value": true}, csrf)
		return code
	}

	// Editor may edit inside the granted subtree...
	if got := set("editor", "group:frontoffice", "test-csrf"); got != 200 {
		t.Errorf("editor at own scope = %d, want 200", got)
	}
	if got := set("editor", "device:lt-1", "test-csrf"); got != 200 {
		t.Errorf("editor at device in subtree = %d, want 200", got)
	}
	// ...but not above or beside it.
	if got := set("editor", "org", "test-csrf"); got != 403 {
		t.Errorf("editor at org = %d, want 403", got)
	}
	if got := set("editor", "group:elsewhere", "test-csrf"); got != 403 {
		t.Errorf("editor at sibling = %d, want 403", got)
	}
	// Owner of the parent group covers the subtree.
	if got := set("owner", "group:frontoffice", "test-csrf"); got != 200 {
		t.Errorf("parent owner = %d, want 200", got)
	}
	// Viewer may read but never write.
	if got := set("auditor", "group:frontoffice", "test-csrf"); got != 403 {
		t.Errorf("viewer writes = %d, want 403", got)
	}
	if code, _ := sessionCall(t, srv, "auditor", "GET", "/api/v1/devices/lt-1", nil, ""); code != 200 {
		t.Errorf("viewer read = %d, want 200", code)
	}
	// No role: no reads either.
	if code, _ := sessionCall(t, srv, "nobody", "GET", "/api/v1/devices", nil, ""); code != 403 {
		t.Errorf("roleless read = %d, want 403", code)
	}
	// CSRF required for session mutations.
	if got := set("editor", "group:frontoffice", ""); got != 403 {
		t.Errorf("missing csrf = %d, want 403", got)
	}
	if got := set("editor", "group:frontoffice", "wrong"); got != 403 {
		t.Errorf("wrong csrf = %d, want 403", got)
	}

	// Commits carry the session author, not the service account.
	out, err := exec.Command("git", "-C", dir, "log", "--format=%an <%ae>").CombinedOutput()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(out), "Edith Editor <edith@x>") {
		t.Errorf("session author missing from git log:\n%s", out)
	}
}

func TestSessionRBACOnPoliciesAndAccess(t *testing.T) {
	srv, _ := newRBACServer(t)

	// Policy CRUD needs org owner: a group owner is refused.
	code, _ := sessionCall(t, srv, "owner", "PUT", "/api/v1/policies/p1",
		map[string]any{"settings": map[string]any{"x": 1}}, "test-csrf")
	if code != 403 {
		t.Errorf("group owner creating org policy = %d, want 403", code)
	}
	// Assignment to the owner's subtree is allowed once the policy exists...
	// (create it as the service principal path is disabled here, so grant an
	// org owner binding first via the owner's own scope: not permitted ->
	// proves delegation boundaries).
	code, _ = sessionCall(t, srv, "owner", "POST", "/api/v1/access",
		map[string]any{"group": "helpers", "role": "editor", "scope": "group:frontoffice"}, "test-csrf")
	if code != 200 {
		t.Errorf("subtree delegation by group owner = %d, want 200", code)
	}
	code, _ = sessionCall(t, srv, "owner", "POST", "/api/v1/access",
		map[string]any{"group": "helpers", "role": "owner", "scope": "org"}, "test-csrf")
	if code != 403 {
		t.Errorf("org grant by group owner = %d, want 403 (privilege escalation)", code)
	}
	// Editor cannot grant at all.
	code, _ = sessionCall(t, srv, "editor", "POST", "/api/v1/access",
		map[string]any{"group": "x", "role": "viewer", "scope": "group:frontoffice"}, "test-csrf")
	if code != 403 {
		t.Errorf("editor granting = %d, want 403", code)
	}
	// Revoke by the delegating owner.
	code, _ = sessionCall(t, srv, "owner", "DELETE", "/api/v1/access",
		map[string]any{"group": "helpers", "scope": "group:frontoffice"}, "test-csrf")
	if code != 200 {
		t.Errorf("revoke = %d, want 200", code)
	}
}

func TestServiceTokenRemainsOwner(t *testing.T) {
	svc, _ := seededService(t, rbacSeed)
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{}, testToken, true, discardLog()).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	// The bearer token is the service principal: owner everywhere, no CSRF.
	code, _ := call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "x", "value": 1}, testToken)
	if code != 200 {
		t.Fatalf("service write = %d, want 200", code)
	}
}

// fakeTokenAuth injects token principals by test header value.
type fakeTokenAuth struct {
	users   map[string]identity.User
	ceiling map[string]identity.Role
}

func (f *fakeTokenAuth) Authenticate(_ context.Context, secret string) (identity.User, identity.Role, bool) {
	u, ok := f.users[secret]
	if !ok {
		return identity.User{}, identity.None, false
	}
	return u, f.ceiling[secret], true
}

// TestTokenCeilingNarrowsNeverWidens: a personal token acts as its owner,
// and a ceiling can only reduce the owner's rights (ADR 0008).
func TestTokenCeilingNarrowsNeverWidens(t *testing.T) {
	svc, _ := seededService(t, rbacSeed)
	ta := &fakeTokenAuth{
		users: map[string]identity.User{
			// owner at zaanstad, no ceiling: full owner in the subtree.
			"tok-owner": {Subject: "ow", Name: "Oscar", Groups: []string{"za-owners"}},
			// same owner, viewer ceiling: reads only.
			"tok-capped": {Subject: "ow", Name: "Oscar", Groups: []string{"za-owners"}},
			// editor owner trying to escalate via a token: impossible, the
			// token carries only the owner's groups.
			"tok-editor": {Subject: "ed", Name: "Edith", Groups: []string{"fo-editors"}},
		},
		ceiling: map[string]identity.Role{
			"tok-capped": identity.Viewer,
		},
	}
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{Tokens: ta}, "", true, discardLog()).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	bearer := func(tok string) func(*http.Request) {
		return func(r *http.Request) { r.Header.Set("Authorization", "Bearer "+tok) }
	}
	do := func(tok, method, path string, body any) int {
		var rd *bytes.Reader
		if body != nil {
			b, _ := json.Marshal(body)
			rd = bytes.NewReader(b)
		}
		var req *http.Request
		if rd != nil {
			req, _ = http.NewRequest(method, srv.URL+path, rd)
		} else {
			req, _ = http.NewRequest(method, srv.URL+path, nil)
		}
		bearer(tok)(req)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	set := map[string]any{"scope": "group:frontoffice", "key": "apps.office", "value": true}

	// Owner token: may write in its subtree.
	if code := do("tok-owner", "POST", "/api/v1/settings", set); code != 200 {
		t.Errorf("owner token write = %d, want 200", code)
	}
	// Capped token: same owner, but viewer ceiling blocks the write...
	if code := do("tok-capped", "POST", "/api/v1/settings", set); code != 403 {
		t.Errorf("viewer-capped token write = %d, want 403", code)
	}
	// ...yet still reads.
	if code := do("tok-capped", "GET", "/api/v1/devices/lt-1", nil); code != 200 {
		t.Errorf("viewer-capped token read = %d, want 200", code)
	}
	// Editor's token cannot reach owner-only routes (policies).
	if code := do("tok-editor", "PUT", "/api/v1/policies/p", map[string]any{
		"settings": map[string]any{"x": 1}}); code != 403 {
		t.Errorf("editor token creating policy = %d, want 403", code)
	}
	// Unknown token: 401.
	if code := do("ghost", "GET", "/api/v1/devices", nil); code != 401 {
		t.Errorf("unknown token = %d, want 401", code)
	}
}

// apiMemTokenStore is a tiny in-memory ports.TokenStore for API tests.
type apiMemTokenStore struct{ m map[string]tokenpkg.Token }

func newAPIMemTokenStore() *apiMemTokenStore {
	return &apiMemTokenStore{m: map[string]tokenpkg.Token{}}
}
func (s *apiMemTokenStore) Put(_ context.Context, t tokenpkg.Token) error { s.m[t.ID] = t; return nil }
func (s *apiMemTokenStore) Get(_ context.Context, id string) (tokenpkg.Token, bool, error) {
	t, ok := s.m[id]
	return t, ok, nil
}
func (s *apiMemTokenStore) ListBySubject(_ context.Context, subj string) ([]tokenpkg.Token, error) {
	var out []tokenpkg.Token
	for _, t := range s.m {
		if t.Subject == subj {
			out = append(out, t)
		}
	}
	return out, nil
}
func (s *apiMemTokenStore) Delete(_ context.Context, id string) error              { delete(s.m, id); return nil }
func (s *apiMemTokenStore) TouchLastUsed(context.Context, string, time.Time) error { return nil }

// TestNoTokenChaining: a scoped token cannot mint further tokens (expiry
// would be extendable forever); sessions and break-glass can.
func TestNoTokenChaining(t *testing.T) {
	svc, _ := seededService(t, rbacSeed)
	toks := app.NewTokenService(newAPIMemTokenStore(), tickClock{}, 0)
	ta := &fakeTokenAuth{users: map[string]identity.User{
		"tok-owner": {Subject: "ow", Name: "Oscar", Groups: []string{"za-owners"}},
	}, ceiling: map[string]identity.Role{}}
	sessions := &fakeSessions{csrf: "csrf", users: map[string]identity.User{
		"oscar": {Subject: "ow", Name: "Oscar", Groups: []string{"za-owners"}},
	}}
	mux := http.NewServeMux()
	New(Services{Config: svc, Tokens: toks}, Authz{Sessions: sessions, Tokens: ta},
		testToken, true, discardLog()).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	body := map[string]any{"id": "chain", "name": "chained"}

	// Via scoped token: refused.
	req, _ := http.NewRequest("POST", srv.URL+"/api/v1/tokens", bytes.NewReader(mustJSON(t, body)))
	req.Header.Set("Authorization", "Bearer tok-owner")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("token minting token = %d, want 403", resp.StatusCode)
	}

	// Via session: allowed.
	if code, out := sessionCall(t, srv, "oscar", "POST", "/api/v1/tokens", body, "csrf"); code != 201 {
		t.Errorf("session minting token = %d, want 201 (%v)", code, out)
	}

	// Via break-glass: allowed (bootstrap path).
	body2 := map[string]any{"id": "boot", "name": "bootstrap"}
	req2, _ := http.NewRequest("POST", srv.URL+"/api/v1/tokens", bytes.NewReader(mustJSON(t, body2)))
	req2.Header.Set("Authorization", "Bearer "+testToken)
	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	resp2.Body.Close()
	if resp2.StatusCode != 201 {
		t.Errorf("break-glass minting = %d, want 201", resp2.StatusCode)
	}
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
