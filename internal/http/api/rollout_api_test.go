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
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// rollout_api_test.go covers the four rollout endpoints, which were at 0%.
//
// Three of the four are Owner-only, and that is the property worth pinning:
// starting, ticking and cancelling a fleet-wide rollout are the most
// consequential buttons in the API, and the difference between Owner and
// Editor there is the difference between "an editor can change a setting"
// and "an editor can push it to every machine tonight".

// rolloutSeed adds a two-ring plan the seed above does not carry.
const rolloutSeed = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"canary": {}, "fleet": {}},
  "devices": {"lt-1": {"groups": ["canary"], "hardware": "hp-g4", "class": "laptop"}},
  "rollout": {"rings": [{"group": "canary", "soakMinutes": 60}, {"group": "fleet"}]}
}`

// noConvergence reports nothing observed: the API tests are about the HTTP
// surface and authorisation, not about whether a wave promotes.
type noConvergence struct{}

func (noConvergence) RingStatus(context.Context, []string, string) (rollout.RingStatus, error) {
	return rollout.RingStatus{}, nil
}

func newRolloutAPI(t *testing.T, write bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": rolloutSeed, "catalog.json": seedCatalog} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
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
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	rs := app.NewRolloutService(svc, st.Rollouts(), noConvergence{}, app.SystemClock{}, log)

	mux := http.NewServeMux()
	New(Services{Config: svc, Rollouts: rs}, Authz{}, testToken, write, log).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestRolloutStatusBeforeAnyRun(t *testing.T) {
	srv := newRolloutAPI(t, true)
	code, body := call(t, srv, "GET", "/api/v1/rollout", nil, testToken)
	if code != 200 {
		t.Fatalf("status = %d: %v", code, body)
	}
	// An idle console must answer "not active" rather than 404 or an empty
	// document: the console polls this, and a 404 there reads as a broken
	// endpoint rather than as a quiet fleet.
	if active, _ := body["active"].(bool); active {
		t.Error("no run has been started, yet the API reports one is active")
	}
}

func TestRolloutStartTickAndCancel(t *testing.T) {
	srv := newRolloutAPI(t, true)

	code, body := call(t, srv, "POST", "/api/v1/rollout", map[string]any{"target": "rev-2"}, testToken)
	if code != http.StatusCreated {
		t.Fatalf("start = %d: %v", code, body)
	}

	code, body = call(t, srv, "GET", "/api/v1/rollout", nil, testToken)
	if code != 200 {
		t.Fatalf("status = %d", code)
	}
	if active, _ := body["active"].(bool); !active {
		t.Error("a started run does not read as active")
	}
	if body["rings"] == nil {
		t.Error("status carries no rings; the console cannot draw the plan")
	}

	// Starting a second run while one is active must be refused, not queued:
	// two runs moving the same ring refs would fight.
	if code, _ = call(t, srv, "POST", "/api/v1/rollout", map[string]any{"target": "rev-3"}, testToken); code == http.StatusCreated {
		t.Error("a second concurrent run was accepted")
	}

	if code, body = call(t, srv, "POST", "/api/v1/rollout/tick", nil, testToken); code != 200 {
		t.Fatalf("tick = %d: %v", code, body)
	}
	if body["state"] == nil {
		t.Error("tick returned no state; the console cannot show what happened")
	}

	if code, body = call(t, srv, "DELETE", "/api/v1/rollout", nil, testToken); code != 200 {
		t.Fatalf("cancel = %d: %v", code, body)
	}
	if code, body = call(t, srv, "GET", "/api/v1/rollout", nil, testToken); code != 200 {
		t.Fatalf("status after cancel = %d", code)
	}
	if active, _ := body["active"].(bool); active {
		t.Errorf("the run is still active after a cancel: %v", body)
	}

	// Cancelling with nothing running is refused rather than silently fine.
	if code, _ = call(t, srv, "DELETE", "/api/v1/rollout", nil, testToken); code == 200 {
		t.Error("cancelling an already cancelled run reported success")
	}
}

func TestRolloutWritesAreRefusedOnAReadOnlyDeployment(t *testing.T) {
	srv := newRolloutAPI(t, false)
	if code, _ := call(t, srv, "GET", "/api/v1/rollout", nil, testToken); code != 200 {
		t.Errorf("read-only deployment refuses to report status: %d", code)
	}
	for _, c := range []struct{ method, path string }{
		{"POST", "/api/v1/rollout"},
		{"POST", "/api/v1/rollout/tick"},
		{"DELETE", "/api/v1/rollout"},
	} {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			code, _ := call(t, srv, c.method, c.path, map[string]any{"target": "rev-2"}, testToken)
			if code == 200 || code == http.StatusCreated {
				t.Errorf("a rollout write succeeded on a read-only deployment (%d)", code)
			}
		})
	}
}

func TestRolloutEndpointsRequireAToken(t *testing.T) {
	srv := newRolloutAPI(t, true)
	if code, _ := call(t, srv, "GET", "/api/v1/rollout", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated status = %d, want 401", code)
	}
	if code, _ := call(t, srv, "POST", "/api/v1/rollout",
		map[string]any{"target": "x"}, "nope"); code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", code)
	}
}
