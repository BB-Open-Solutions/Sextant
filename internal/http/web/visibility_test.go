package web_test

import (
	"context"
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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

const visSeed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"alpha": {}, "beta": {}},
  "devices": {
    "a-1": {"groups": ["alpha"], "hardware": "hw"},
    "b-1": {"groups": ["beta"], "hardware": "hw"}
  },
  "access": [{"group": "alpha-team", "role": "viewer", "scope": "group:alpha"}]
}`

// scopedSessions is a session source for one fixed non-service user.
type scopedSessions struct{ u identity.User }

func (s scopedSessions) SessionUser(*http.Request) (identity.User, string, bool) {
	return s.u, "csrf", true
}

func newScopedConsole(t *testing.T, u identity.User) *httptest.Server {
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
	srv, err := web.New(web.Services{Config: cfg}, scopedSessions{u}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestConsoleReadConfidentiality(t *testing.T) {
	ts := newScopedConsole(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})
	fetch := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Devices page lists only the visible subtree.
	code, page := fetch("/devices")
	if code != 200 || !strings.Contains(page, "a-1") {
		t.Fatalf("devices = %d, own device missing", code)
	}
	for _, leak := range []string{"b-1", "beta"} {
		if strings.Contains(page, leak) {
			t.Errorf("devices page leaked %q", leak)
		}
	}

	// An invisible device page answers exactly like a missing one.
	if code, _ := fetch("/devices/b-1"); code != 404 {
		t.Errorf("invisible device page = %d, want 404", code)
	}

	// Settings: invisible scope 404s; the selector hides the sibling group.
	if code, _ := fetch("/settings?scope=group:beta"); code != 404 {
		t.Errorf("invisible scope = %d, want 404", code)
	}
	code, page = fetch("/settings")
	if code != 200 || !strings.Contains(page, "group:alpha") {
		t.Fatalf("settings = %d, own group missing from selector", code)
	}
	if strings.Contains(page, "group:beta") {
		t.Error("settings selector leaked sibling group")
	}
}
