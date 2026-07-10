package api

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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

type fakeDirectory struct{ gotQuery string }

func (f *fakeDirectory) ListGroups(_ context.Context, q string) ([]ports.DirectoryGroup, error) {
	f.gotQuery = q
	return []ports.DirectoryGroup{{ID: "cn=admins,dc=x", Name: "admins"}}, nil
}

// TestDirectoryBrowseAuthz: owners browse, viewers do not, unconfigured 403s.
func TestDirectoryBrowseAuthz(t *testing.T) {
	// Owner at a group: allowed.
	srv := newVisAPIWith(t, identity.User{Subject: "u", Groups: []string{"alpha-owners"}},
		func(a *API) { a.dir = &fakeDirectory{} },
		`{"group": "alpha-owners", "role": "owner", "scope": "group:alpha"}`)
	code, body := get(t, srv, "/api/v1/directory/groups?q=adm")
	if code != 200 {
		t.Fatalf("owner browse = %d %s", code, body)
	}

	// Viewer: refused.
	srv = newVisAPIWith(t, identity.User{Subject: "v", Groups: []string{"alpha-team"}},
		func(a *API) { a.dir = &fakeDirectory{} }, "")
	if code, _ := get(t, srv, "/api/v1/directory/groups"); code != 403 {
		t.Fatalf("viewer browse = %d, want 403", code)
	}

	// No directory configured: refused with a clear reason.
	srv = newVisAPIWith(t, identity.User{Subject: "u", Groups: []string{"org-team"}}, nil, "")
	if code, _ := get(t, srv, "/api/v1/directory/groups"); code != 403 {
		t.Fatalf("unconfigured browse = %d, want 403", code)
	}
}

// newVisAPIWith is newVisAPI plus an optional extra access binding in the
// seed and a hook to mutate the API before routing (e.g. inject a fake
// directory).
func newVisAPIWith(t *testing.T, u identity.User, mod func(*API), extraBinding string) *httptest.Server {
	t.Helper()
	seed := visSeed
	if extraBinding != "" {
		seed = strings.Replace(seed, `"access": [`, `"access": [`+extraBinding+",", 1)
	}
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seed), 0o644); err != nil {
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
	a := New(Services{Config: svc}, Authz{Sessions: visSessions{u}}, "", false,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if mod != nil {
		mod(a)
	}
	mux := http.NewServeMux()
	a.Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}
