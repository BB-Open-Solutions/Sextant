package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// seedFleetAccess has a group bound to a role, so a service account minted
// with that group resolves to a real org role and the group is offerable.
const seedFleetAccess = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}},
  "access": [{"group": "ci", "role": "editor", "scope": "org"}]
}`

// newSvcAcctConsole builds a console with the token service over a seeded
// repo that already has an access binding for group "ci".
func newSvcAcctConsole(t *testing.T) (*httptest.Server, *memTokens) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedFleetAccess), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	tokens := &memTokens{toks: map[string]token.Token{}}
	srv, err := web.New(web.Services{
		Config: cfg,
		Tokens: app.NewTokenService(tokens, clockNow{}, 0),
	}, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, tokens
}

func TestServiceAccountMintListRevoke(t *testing.T) {
	ts, store := newSvcAcctConsole(t)
	c := client()

	// Page renders for the owner with the mint form and the bindable group.
	resp, _ := c.Get(ts.URL + "/service-accounts")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Create a service account") {
		t.Fatalf("page = %d\n%s", resp.StatusCode, body)
	}
	if !strings.Contains(string(body), `value="ci"`) {
		t.Fatal("bound group ci not offered as a choice")
	}

	// Mint a service account bound to ci; an unbound group in the same POST
	// must be dropped (it cannot silently grant unbound rights).
	form := url.Values{"csrf": {"dev-csrf"}, "id": {"ci-runner"}, "name": {"CI runner"},
		"groups": {"ci", "smuggled"}, "ttlDays": {"30"}}
	resp, _ = c.PostForm(ts.URL+"/service-accounts", form)
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("mint = %d, want 303", resp.StatusCode)
	}
	tok, ok, _ := store.Get(context.Background(), "ci-runner")
	if !ok {
		t.Fatal("service account not stored")
	}
	if tok.Kind != token.Service || tok.Subject != "svc:ci-runner" {
		t.Fatalf("stored token kind=%q subject=%q", tok.Kind, tok.Subject)
	}
	if len(tok.Groups) != 1 || tok.Groups[0] != "ci" {
		t.Fatalf("groups = %v, want only [ci] (unbound group must be dropped)", tok.Groups)
	}

	// It appears in the list with its subject and resolved editor role.
	resp, _ = c.Get(ts.URL + "/service-accounts")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "svc:ci-runner") || !strings.Contains(string(body), "editor") {
		t.Fatalf("account not listed with role\n%s", body)
	}

	// Revoke it.
	resp, _ = c.PostForm(ts.URL+"/service-accounts/ci-runner/revoke", url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("revoke = %d, want 303", resp.StatusCode)
	}
	if _, ok, _ := store.Get(context.Background(), "ci-runner"); ok {
		t.Fatal("service account still present after revoke")
	}
}

// TestServiceAccountRevokeRejectsNonService proves this owner surface cannot
// be used to revoke a personal or device credential by id.
func TestServiceAccountRevokeRejectsNonService(t *testing.T) {
	ts, store := newSvcAcctConsole(t)
	c := client()

	// Seed a personal token directly in the store.
	personal, _, err := token.Mint("pt-x", "mine", token.Personal, "sub-dev", nil, "", clockNow{}.Now(), 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Put(context.Background(), personal); err != nil {
		t.Fatal(err)
	}

	resp, _ := c.PostForm(ts.URL+"/service-accounts/pt-x/revoke", url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("non-service token was revoked via the service-account surface")
	}
	if _, ok, _ := store.Get(context.Background(), "pt-x"); !ok {
		t.Fatal("personal token was deleted by the service-account revoke")
	}
}
