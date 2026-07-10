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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
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
