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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// ladderSeed is a fleet with a test group plus four production groups of
// growing size (1+2+3+4 = 10 ladder devices), so percentage derivation has
// real bins to pack.
const ladderSeed = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"test": {}, "tiny": {}, "small": {}, "mid": {}, "big": {}},
  "devices": {
    "t-1": {"groups": ["test"]},
    "ti-1": {"groups": ["tiny"]},
    "sm-1": {"groups": ["small"]}, "sm-2": {"groups": ["small"]},
    "mi-1": {"groups": ["mid"]}, "mi-2": {"groups": ["mid"]}, "mi-3": {"groups": ["mid"]},
    "bi-1": {"groups": ["big"]}, "bi-2": {"groups": ["big"]}, "bi-3": {"groups": ["big"]}, "bi-4": {"groups": ["big"]}
  }
}`

type stubConvergence struct{ rs rollout.RingStatus }

func (c *stubConvergence) RingStatus(context.Context, []string, string) (rollout.RingStatus, error) {
	return c.rs, nil
}

type stubClock struct{ t time.Time }

func (c *stubClock) Now() time.Time { return c.t }

// newUpdatesConsole wires the console WITH a live rollout service over the
// ladder fleet, so the Updates pages and the run controls are exercised
// end-to-end (store, engine and handlers together).
func newUpdatesConsole(t *testing.T) (*httptest.Server, *app.ConfigService, *app.RolloutService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": ladderSeed, "catalog.json": seedCatalog} {
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
	cfg, err := app.NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	clock := &stubClock{time.Now()}
	rolloutSvc := app.NewRolloutService(cfg, st.Rollouts(), &stubConvergence{}, clock, log)
	// The Updates board lists change requests, so the page needs a real
	// change service even though these tests never open one.
	openWT := func(dir string) (ports.ConfigRepo, error) { return git.Open(dir, "") }
	changeSvc := app.NewChangeService(repo, st.Changes(), ports.GateFunc(func(context.Context, string, []string) error { return nil }),
		nil, clock, openWT, cfg)
	srv, err := web.New(web.Services{Config: cfg, Rollouts: rolloutSvc, Changes: changeSvc}, web.DevSessions{}, true,
		nil, nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg, rolloutSvc
}

func postForm(t *testing.T, ts *httptest.Server, path string, form url.Values) int {
	t.Helper()
	form.Set("csrf", "dev-csrf")
	resp, err := client().PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp.StatusCode
}

func getPage(t *testing.T, ts *httptest.Server, path string) (int, string) {
	t.Helper()
	resp, err := client().Get(ts.URL + path)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestOrgUpdatesPolicyDerivesPercentageWaves(t *testing.T) {
	ts, cfg, _ := newUpdatesConsole(t)

	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"10, 30, 60"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	f := cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) < 2 {
		t.Fatalf("plan = %+v", f.Rollout)
	}
	r0 := f.Rollout.Rings[0]
	if r0.Group != "test" || !r0.RequireApproval {
		t.Fatalf("ring 0 must be the gated test wave, got %+v", r0)
	}
	covered := map[string]bool{}
	for _, ring := range f.Rollout.Rings[1:] {
		if !strings.Contains(ring.Name, "%") {
			t.Errorf("percentage wave without share in its name: %+v", ring)
		}
		for _, g := range ring.GroupList() {
			covered[g] = true
		}
	}
	for _, g := range []string{"tiny", "small", "mid", "big"} {
		if !covered[g] {
			t.Errorf("group %s missing from the ladder", g)
		}
	}

	// The policy page previews the derived plan and echoes the ladder shape.
	code, page := getPage(t, ts, "/org/updates")
	if code != 200 {
		t.Fatalf("org/updates = %d", code)
	}
	for _, want := range []string{"Wave 1", "devices", `name="percents"`, "Testgroep"} {
		if !strings.Contains(page, want) {
			t.Errorf("policy page missing %q", want)
		}
	}
}

func TestOrgUpdatesPolicyRejectsBadInput(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"ghost"}, "percents": {"10, 90"},
	}); code != 400 {
		t.Errorf("unknown test group = %d, want 400", code)
	}
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"tien procent"},
	}); code != 400 {
		t.Errorf("unparseable percents = %d, want 400", code)
	}
}

func TestUpdatesRunControlsPauseResumeCancel(t *testing.T) {
	ts, _, rolloutSvc := newUpdatesConsole(t)

	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	if code := postForm(t, ts, "/rollout", url.Values{"target": {"deadbeef"}}); code != 303 {
		t.Fatalf("start = %d", code)
	}

	// Overview and monitor render the running state.
	if code, page := getPage(t, ts, "/updates"); code != 200 || !strings.Contains(page, "deadbeef") {
		t.Errorf("updates overview: code %d, target shown: %v", code, strings.Contains(page, "deadbeef"))
	}
	if code, _ := getPage(t, ts, "/updates/rollout"); code != 200 {
		t.Errorf("monitor = %d", code)
	}

	assertStatus := func(want rollout.RunStatus) {
		t.Helper()
		st, _, err := rolloutSvc.Status(context.Background())
		if err != nil || st == nil {
			t.Fatalf("status: %v (%v)", st, err)
		}
		if st.Status != want {
			t.Fatalf("run status = %s, want %s", st.Status, want)
		}
	}
	if code := postForm(t, ts, "/rollout/pause", url.Values{}); code != 303 {
		t.Fatalf("pause = %d", code)
	}
	assertStatus(rollout.Paused)
	if code := postForm(t, ts, "/rollout/resume", url.Values{}); code != 303 {
		t.Fatalf("resume = %d", code)
	}
	assertStatus(rollout.Active)
	if code := postForm(t, ts, "/rollout/cancel", url.Values{}); code != 303 {
		t.Fatalf("cancel = %d", code)
	}
	assertStatus(rollout.Cancelled)

	// A cancelled run is history: the monitor says so, offers no steering
	// controls, and points back at the overview for a fresh start.
	code, page := getPage(t, ts, "/updates/rollout")
	if code != 200 {
		t.Fatalf("monitor after cancel = %d", code)
	}
	for _, want := range []string{"This rollout has ended", "Stopped"} {
		if !strings.Contains(page, want) {
			t.Errorf("terminal monitor missing %q", want)
		}
	}
	for _, forms := range []string{`action="/rollout/cancel"`, `action="/rollout/approve"`, `action="/rollout/pause"`} {
		if strings.Contains(page, forms) {
			t.Errorf("terminal monitor still offers %s", forms)
		}
	}
}

func TestPipelineAndRolloutRedirectToUpdates(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	for path, want := range map[string]string{"/pipeline": "/updates", "/rollout": "/updates/rollout"} {
		resp, err := client().Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 301 || resp.Header.Get("Location") != want {
			t.Errorf("%s -> %d %s, want 301 %s", path, resp.StatusCode, resp.Header.Get("Location"), want)
		}
	}
}
