package api

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// visSeed: two sibling subtrees; alpha-team may view group alpha only.
const visSeed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"alpha": {}, "beta": {}},
  "devices": {
    "a-1": {"groups": ["alpha"], "hardware": "hw"},
    "b-1": {"groups": ["beta"], "hardware": "hw"}
  },
  "policies": {"pol-beta": {"settings": {"apps.media": true}}},
  "assignments": [{"policy": "pol-beta", "target": "group:beta"}],
  "access": [
    {"group": "alpha-team", "role": "viewer", "scope": "group:alpha"},
    {"group": "org-team", "role": "viewer", "scope": "org"}
  ]
}`

// visSessions authenticates every request as a fixed session user.
type visSessions struct{ u identity.User }

func (f visSessions) SessionUser(*http.Request) (identity.User, string, bool) {
	return f.u, "csrf", true
}

func newVisAPI(t *testing.T, u identity.User) *httptest.Server {
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
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{Sessions: visSessions{u}}, "", false,
		slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func get(t *testing.T, srv *httptest.Server, path string) (int, []byte) {
	t.Helper()
	resp, err := http.Get(srv.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, b
}

func TestReadConfidentialityScopedViewer(t *testing.T) {
	srv := newVisAPI(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})

	// getFleet: the sibling subtree and its policy machinery are gone.
	code, body := get(t, srv, "/api/v1/fleet")
	if code != 200 {
		t.Fatalf("fleet = %d", code)
	}
	var doc struct {
		Groups      map[string]any `json:"groups"`
		Devices     map[string]any `json:"devices"`
		Policies    map[string]any `json:"policies"`
		Assignments []any          `json:"assignments"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatal(err)
	}
	if _, ok := doc.Groups["beta"]; ok {
		t.Error("sibling group leaked")
	}
	if _, ok := doc.Groups["alpha"]; !ok {
		t.Error("own group missing")
	}
	if _, ok := doc.Devices["b-1"]; ok {
		t.Error("sibling device leaked")
	}
	if _, ok := doc.Policies["pol-beta"]; ok {
		t.Error("sibling policy leaked")
	}
	if len(doc.Assignments) != 0 {
		t.Errorf("sibling assignment leaked: %v", doc.Assignments)
	}

	// Device list contains only visible devices.
	code, body = get(t, srv, "/api/v1/devices")
	var list []struct {
		Tag string `json:"tag"`
	}
	_ = json.Unmarshal(body, &list)
	if code != 200 || len(list) != 1 || list[0].Tag != "a-1" {
		t.Fatalf("devices = %d %s", code, body)
	}

	// An invisible device answers exactly like a missing one.
	if code, _ := get(t, srv, "/api/v1/devices/b-1"); code != 404 {
		t.Fatalf("invisible device = %d, want 404", code)
	}
	if code, _ := get(t, srv, "/api/v1/devices/a-1"); code != 200 {
		t.Fatalf("own device = %d, want 200", code)
	}
}

func TestReadConfidentialityOrgViewerSeesAll(t *testing.T) {
	srv := newVisAPI(t, identity.User{Subject: "o", Groups: []string{"org-team"}})
	code, body := get(t, srv, "/api/v1/fleet")
	if code != 200 {
		t.Fatalf("fleet = %d", code)
	}
	var doc struct {
		Groups  map[string]any `json:"groups"`
		Devices map[string]any `json:"devices"`
	}
	_ = json.Unmarshal(body, &doc)
	if len(doc.Groups) != 2 || len(doc.Devices) != 2 {
		t.Fatalf("org viewer filtered: %s", body)
	}
}
