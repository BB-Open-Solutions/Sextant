package web_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// forge_page_test.go tests the WIRING of the forge-credential page, not the
// service underneath (that has its own tests in internal/app). The two things
// worth pinning here are the ones a service test cannot see: that the page is
// owner-only, and that the token never reaches the browser.

type forgeMem struct {
	id  forge.Identity
	has bool
}

func (m *forgeMem) GetForgeIdentity(context.Context, string) (forge.Identity, bool, error) {
	return m.id, m.has, nil
}
func (m *forgeMem) PutForgeIdentity(_ context.Context, _ string, id forge.Identity) error {
	m.id, m.has = id, true
	return nil
}
func (m *forgeMem) DeleteForgeIdentity(context.Context, string) error {
	m.id, m.has = forge.Identity{}, false
	return nil
}

// newForgeConfig seeds a minimal repo-backed ConfigService: the console needs
// one to render any page, and this test is not about its contents.
func newForgeConfig(t *testing.T) *app.ConfigService {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(visSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte(seedCatalog), 0o644); err != nil {
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
	return cfg
}

func newForgeConsole(t *testing.T, u identity.User) (*httptest.Server, *app.ForgeIdentityService) {
	t.Helper()
	sealer, err := secretbox.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32)))
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewForgeIdentityService(&forgeMem{}, sealer, "default",
		filepath.Join(t.TempDir(), ".netrc"), nil)
	srv, err := web.New(web.Services{Config: newForgeConfig(t), ForgeID: svc},
		scopedSessions{u}, true, nil, nil, []string{"owners"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, svc
}

func TestForgePageIsOwnerOnly(t *testing.T) {
	ts, _ := newForgeConsole(t, identity.User{Subject: "u", Groups: []string{"someone-else"}})
	resp, err := http.Get(ts.URL + "/org/forge")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("non-owner GET /org/forge = %d, want 403", resp.StatusCode)
	}
}

func TestForgePageNeverRendersTheToken(t *testing.T) {
	ts, svc := newForgeConsole(t, identity.User{Subject: "u", Groups: []string{"owners"}})
	const secret = "tok-do-not-render-me"
	if err := svc.Set(context.Background(), "forge.example.org", "sextant-bot", secret, "bram"); err != nil {
		t.Fatalf("set: %v", err)
	}
	resp, err := http.Get(ts.URL + "/org/forge")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	body := string(b)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("owner GET /org/forge = %d\n%s", resp.StatusCode, body)
	}
	if strings.Contains(body, secret) {
		t.Error("the page rendered the stored token")
	}
	// It does say which account is in use - a rotation page that cannot tell
	// you what it is about to replace is not much of one.
	if !strings.Contains(body, "sextant-bot") || !strings.Contains(body, "forge.example.org") {
		t.Errorf("the page does not name the account in use:\n%s", body)
	}
	// And it names who rotated it, which is the audit answer H2 was missing.
	if !strings.Contains(body, "bram") {
		t.Error("the page does not say who last replaced the credential")
	}
}

// TestForgePageSaysWhyItCannotStore covers the deployment without a sealing
// key: the admin must learn the precondition, not meet a dead form.
func TestForgePageSaysWhyItCannotStore(t *testing.T) {
	sealer, err := secretbox.New("")
	if err != nil {
		t.Fatal(err)
	}
	svc := app.NewForgeIdentityService(&forgeMem{}, sealer, "default",
		filepath.Join(t.TempDir(), ".netrc"), nil)
	srv, err := web.New(web.Services{Config: newForgeConfig(t), ForgeID: svc},
		scopedSessions{identity.User{Subject: "u", Groups: []string{"owners"}}},
		true, nil, nil, []string{"owners"},
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)

	resp, err := http.Get(ts.URL + "/org/forge")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(b), "SEXTANT_SECRET_KEY") {
		t.Errorf("the page does not name the missing precondition:\n%s", b)
	}
}
