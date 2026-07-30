package web_test

import (
	"context"
	"fmt"
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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
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

// ladderLock pins a core revision the way an overlay's flake.lock does, so the
// board can name the one version that IS a version (2025-06-23 in UTC).
const ladderLock = `{
  "nodes": {
    "dawo": {"locked": {"lastModified": 1750680000, "owner": "MinBZK", "repo": "DAWO",
      "rev": "0f1e2d3c4b5a69788796a5b4c3d2e1f001234567", "type": "github"}},
    "root": {"inputs": {"dawo": "dawo"}}
  },
  "root": "root",
  "version": 7
}`

// stubCacheBuilder reports every build as still running, pinning the engine
// in the await-build phase.
type stubCacheBuilder struct{}

func (stubCacheBuilder) EnsureBuilt(context.Context, string, []string) (ports.BuildState, error) {
	return ports.BuildState{Phase: ports.BuildBuilding}, nil
}

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
	return newUpdatesConsoleWith(t, rollout.RingStatus{})
}

// newUpdatesConsoleWith is newUpdatesConsole with a fixed convergence reading,
// so a test can drive the wave counters the board renders.
func newUpdatesConsoleWith(t *testing.T, rs rollout.RingStatus) (*httptest.Server, *app.ConfigService, *app.RolloutService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": ladderSeed, "catalog.json": seedCatalog, "flake.lock": ladderLock} {
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
	rolloutSvc := app.NewRolloutService(cfg, st.Rollouts(), &stubConvergence{rs: rs}, clock, log)
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
	if code := postForm(t, ts, "/rollout", url.Values{"target": {"deadbeef"}, "confirmed": {"1"}}); code != 303 {
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

func TestScopedRolloutSnapshotsItsOwnPlan(t *testing.T) {
	ts, cfg, rolloutSvc := newUpdatesConsole(t)

	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	// A change scoped to one group: test wave plus just that group.
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"cafebabe"}, "scope": {"mid"}, "confirmed": {"1"},
	}); code != 303 {
		t.Fatalf("scoped start = %d", code)
	}
	st, _, err := rolloutSvc.Status(context.Background())
	if err != nil || st == nil {
		t.Fatalf("status: %v (%v)", st, err)
	}
	if len(st.Rings) != 2 {
		t.Fatalf("scoped run rings = %+v, want test wave + scope", st.Rings)
	}
	if st.Rings[0].Group != "test" || !st.Rings[0].RequireApproval {
		t.Errorf("ring 0 = %+v, want the gated test wave", st.Rings[0])
	}
	if got := st.Rings[1].GroupList(); len(got) != 1 || got[0] != "mid" {
		t.Errorf("ring 1 = %v, want [mid]", got)
	}

	// The run owns its snapshot: rewriting the org ladder mid-run must not
	// reshuffle the waves the monitor shows.
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"tiny"}, "percents": {"100"},
	}); code != 303 {
		t.Fatalf("replan = %d", code)
	}
	if _, page := getPage(t, ts, "/updates/rollout"); !strings.Contains(page, "mid") {
		t.Error("monitor lost the scoped run's own waves after a replan")
	}
	if code := postForm(t, ts, "/rollout/cancel", url.Values{}); code != 303 {
		t.Fatalf("cancel = %d", code)
	}

	// An unknown scope group is refused.
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"cafebabe"}, "scope": {"ghost"},
	}); code != 400 {
		t.Errorf("ghost scope = %d, want 400", code)
	}
	_ = cfg
}

func TestExpeditedRunShortensSoakNotEvidence(t *testing.T) {
	ts, _, rolloutSvc := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"feedf00d"}, "expedited": {"1"}, "confirmed": {"1"},
	}); code != 303 {
		t.Fatalf("expedited start = %d", code)
	}
	st, _, err := rolloutSvc.Status(context.Background())
	if err != nil || st == nil {
		t.Fatalf("status: %v (%v)", st, err)
	}
	for i, ring := range st.Rings {
		if ring.SoakMinutes == 0 || ring.SoakMinutes > 5 {
			t.Errorf("ring %d soak = %d, want 1-5 minutes", i, ring.SoakMinutes)
		}
	}
	// Urgency never skips evidence: the test wave keeps its manual gate.
	if !st.Rings[0].RequireApproval {
		t.Error("expedited run lost the test wave's sign-off gate")
	}
}

// TestRolloutStartRequiresConfirmation proves fix A: the bare POST /rollout
// (no confirmed=1) renders a summary instead of starting anything, and only
// confirmed=1 actually starts the run.
func TestRolloutStartRequiresConfirmation(t *testing.T) {
	ts, _, rolloutSvc := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}

	code, page := postFormBody(t, ts, "/rollout", url.Values{
		"target": {"deadbeef"}, "scope": {"*"}, "expedited": {"1"},
	})
	if code != 200 {
		t.Fatalf("unconfirmed start = %d", code)
	}
	for _, want := range []string{"Confirm rollout", "deadbeef", "Whole fleet", "Wave 1", "Expedited"} {
		if !strings.Contains(page, want) {
			t.Errorf("confirm page missing %q", want)
		}
	}
	if st, _, err := rolloutSvc.Status(context.Background()); err != nil || st != nil {
		t.Fatalf("unconfirmed POST must not start a run: %+v (%v)", st, err)
	}

	// confirmed=1 re-posts the same fields and actually starts the run.
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"deadbeef"}, "scope": {"*"}, "expedited": {"1"}, "confirmed": {"1"},
	}); code != 303 {
		t.Fatalf("confirmed start = %d", code)
	}
	st, _, err := rolloutSvc.Status(context.Background())
	if err != nil || st == nil {
		t.Fatalf("status: %v (%v)", st, err)
	}
	if st.Target != "deadbeef" {
		t.Errorf("target = %q, want deadbeef", st.Target)
	}
}

// TestRolloutStartScopedPreviewMatchesWhatRuns proves the confirmation page
// (fix A) shows the SCOPED plan (test wave + one group) it will actually
// run, not the org-wide default, when a group is chosen.
func TestRolloutStartScopedPreviewMatchesWhatRuns(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	code, page := postFormBody(t, ts, "/rollout", url.Values{
		"target": {"cafebabe"}, "scope": {"mid"},
	})
	if code != 200 {
		t.Fatalf("unconfirmed scoped start = %d", code)
	}
	if !strings.Contains(page, "Only mid") && !strings.Contains(page, "mid") {
		t.Errorf("scoped confirm page does not name the scope group: %s", page)
	}
	// A full-ladder wave that is NOT part of the scoped plan (e.g. "tiny",
	// which only appears if the whole ladder renders) must not leak in.
	if strings.Contains(page, "tiny") {
		t.Errorf("scoped confirm page leaked an unrelated wave's group: %s", page)
	}
}

// TestRolloutMonitorShowsWaveGates proves fix G: each wave card on the
// monitor names its promotion gates (soak minutes, min healthy %), so an
// operator sees what an in-progress wave is waiting on.
func TestRolloutMonitorShowsWaveGates(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"deadbeef"}, "confirmed": {"1"},
	}); code != 303 {
		t.Fatalf("start = %d", code)
	}
	code, page := getPage(t, ts, "/updates/rollout")
	if code != 200 {
		t.Fatalf("monitor = %d", code)
	}
	// The test wave's derived soak is 60 minutes; percentage waves default to
	// 30. Every wave without an explicit healthy threshold shows the 95%
	// default (rollout.Ring's zero-means-95 rule).
	for _, want := range []string{"Gates:", "Soak (min)", "60", "30", "Min healthy %", "95%"} {
		if !strings.Contains(page, want) {
			t.Errorf("monitor missing gates content %q", want)
		}
	}
}

// TestUpdatesScopeSelectForcesAChoice proves fix D: the scope select opens on
// a disabled placeholder (no silent fleet-wide default) and is required, with
// fleet-wide as an explicit, separately-selectable option.
func TestUpdatesScopeSelectForcesAChoice(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"100"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	code, page := getPage(t, ts, "/updates")
	if code != 200 {
		t.Fatalf("updates = %d", code)
	}
	if !strings.Contains(page, `<select name="scope" class="!w-auto" required`) {
		t.Error("scope select is not marked required")
	}
	if !strings.Contains(page, `<option value="" disabled selected>`) {
		t.Error("scope select has no disabled placeholder option")
	}
	if !strings.Contains(page, `<option value="*">`) {
		t.Error("fleet-wide is no longer an explicit, separately-selectable option")
	}
}

// TestUpdatesExpeditedHintIsAlwaysVisible proves fix C: the expedited
// checkbox's explanation is a visible line, not only a hover title.
func TestUpdatesExpeditedHintIsAlwaysVisible(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"100"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	_, page := getPage(t, ts, "/updates")
	if !strings.Contains(page, "Security-fix pace") || !strings.Contains(page, "shrinks every wave") {
		t.Error("expedited checkbox lost its always-visible short hint")
	}
}

// TestUpdatesBoardReadsAsStatusUnderAutoFlow proves ADR 0012's console half:
// with the ladder as standing policy the board states that changes flow by
// themselves and the dispatch button becomes the expedited override; with
// auto-flow off the ordinary wave dispatch returns.
func TestUpdatesBoardReadsAsStatusUnderAutoFlow(t *testing.T) {
	ts, cfg, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"100"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}

	_, page := getPage(t, ts, "/updates")
	if !strings.Contains(page, "Changes flow to the fleet automatically") {
		t.Error("auto-flow board does not say updates flow by themselves")
	}
	if !strings.Contains(page, "Roll out now (expedited)") {
		t.Error("dispatch button is not relabelled as the expedited override")
	}
	if !strings.Contains(page, `<input type="hidden" name="expedited" value="1">`) {
		t.Error("override button does not post the expedited pace")
	}

	// Off switch: the org that wants scheduled windows only gets the plain
	// wave dispatch back.
	off := false
	plan := *cfg.Fleet().Rollout
	plan.AutoFlow = &off
	if err := cfg.Apply(context.Background(), fleet.SetRolloutPlan(&plan),
		"rollout: manual dispatch only", ports.Author{Name: "t"}); err != nil {
		t.Fatal(err)
	}
	_, page = getPage(t, ts, "/updates")
	if strings.Contains(page, "Changes flow to the fleet automatically") {
		t.Error("manual-dispatch org still told updates flow by themselves")
	}
	if !strings.Contains(page, "Roll out changes") {
		t.Error("manual-dispatch org lost the wave dispatch button")
	}
	if !strings.Contains(page, `<input type="checkbox" name="expedited" value="1"`) {
		t.Error("manual-dispatch org lost the expedited choice")
	}
}

// TestUpdatesBoardLeadsWithChangeNotRelease proves audit item 46: the board
// separates the two version axes. Config changes read as a status led by WHAT
// is changing (the release number and revision demoted to the hover title and
// a mono suffix), the core pin is named as the real version it is, and the
// wave counters speak plain language instead of "(1/3 - 1 away)".
func TestUpdatesBoardLeadsWithChangeNotRelease(t *testing.T) {
	ts, cfg, rolloutSvc := newUpdatesConsoleWith(t, rollout.RingStatus{Total: 4, OnTarget: 1, Healthy: 1, Absent: 1})
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"100"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}

	// Idle: the image lineage is the version an operator reads.
	_, page := getPage(t, ts, "/updates")
	for _, want := range []string{"DAWO core", "0f1e2d3c4b5a", "pinned", "2025-06-23"} {
		if !strings.Contains(page, want) {
			t.Errorf("idle board missing core version content %q", want)
		}
	}

	// Rolling out: the change leads, the numbers retreat.
	ctx := context.Background()
	head := cfg.Head(ctx)
	if _, err := rolloutSvc.StartWith(ctx, head,
		app.StartOpts{ChangeTitle: "Office suite update"}, ports.Author{Name: "t"}); err != nil {
		t.Fatal(err)
	}
	_, page = getPage(t, ts, "/updates")
	if !strings.Contains(page, "Office suite update") {
		t.Error("running board does not lead with the change it delivers")
	}
	// The revision identifies what is rolling out and stays available to
	// quote, in the hover title. What must NOT be there is a release number:
	// a configuration change is not a version, and numbering it invents a
	// lineage that makes a policy typo look like a new DAWO.
	if !strings.Contains(page, fmt.Sprintf(`title="%s"`, head)) {
		t.Errorf("the revision is not in the hover title (want title=%q)", head)
	}
	if strings.Contains(page, "Release "+fmt.Sprint(cfg.ReleaseNumber(ctx, head))) {
		t.Error("the board still numbers a configuration change as a release")
	}
	// Plain language, both grammatical numbers, and no jargon left.
	for _, want := range []string{"1 of 3 devices updated", "1 device not reporting"} {
		if !strings.Contains(page, want) {
			t.Errorf("wave line missing plain-language progress %q", want)
		}
	}
	if strings.Contains(page, "(1/3") || strings.Contains(page, "away)") {
		t.Error("wave line still shows the old counter arithmetic")
	}
}

func TestMaintenanceWindowCard(t *testing.T) {
	ts, cfg, _ := newUpdatesConsole(t)

	if code := postForm(t, ts, "/org/updates/window", url.Values{
		"group": {"mid"}, "window": {"22:00-06:00"},
	}); code != 303 {
		t.Fatalf("set window = %d", code)
	}
	if v, _ := cfg.Fleet().Groups["mid"].Settings["updates.maintenanceWindow"].(string); v != "22:00-06:00" {
		t.Fatalf("window not stored: %q", v)
	}
	// The card echoes it back.
	if _, page := getPage(t, ts, "/org/updates"); !strings.Contains(page, "22:00-06:00") {
		t.Error("card does not show the stored window")
	}
	// Bad format refused; empty clears.
	if code := postForm(t, ts, "/org/updates/window", url.Values{
		"group": {"mid"}, "window": {"altijd"},
	}); code != 400 {
		t.Errorf("bad format = %d, want 400", code)
	}
	if code := postForm(t, ts, "/org/updates/window", url.Values{
		"group": {"mid"}, "window": {""},
	}); code != 303 {
		t.Fatalf("clear = %d", code)
	}
	if _, has := cfg.Fleet().Groups["mid"].Settings["updates.maintenanceWindow"]; has {
		t.Error("empty save did not clear the window")
	}
}

// TestRolloutStartRefusesEmptyScope proves the nothing-deployable guard: a
// scope whose waves cover zero active devices is refused outright instead of
// rendering a confirmation page for a no-op run.
func TestRolloutStartRefusesEmptyScope(t *testing.T) {
	ts, _, _ := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	// An org group without devices exists (created via the console).
	if code := postForm(t, ts, "/groups", url.Values{"name": {"empty"}}); code != 303 {
		t.Fatalf("create group = %d", code)
	}
	// Scoping to it: test wave has 1 device, the scope wave 0 - that still
	// updates something, so it confirms. Retire the test device first so the
	// whole plan is empty.
	if code := postForm(t, ts, "/devices/t-1/retire", url.Values{}); code != 303 {
		t.Fatalf("retire = %d", code)
	}
	code, page := postFormBody(t, ts, "/rollout", url.Values{
		"target": {"cafebabe"}, "scope": {"empty"},
	})
	if code != 400 {
		t.Fatalf("empty-scope start = %d, want 400 (page: %.200s)", code, page)
	}
	if !strings.Contains(page, "nothing to roll out") {
		t.Errorf("refusal does not explain itself: %.300s", page)
	}
}

// TestMonitorShowsBuildingPhase: while build-before-promote fills the cache
// (BuildRequestedAt set, ring not yet promoted) the wave says so instead of
// "Deploying" - devices have not been offered the release yet.
func TestMonitorShowsBuildingPhase(t *testing.T) {
	ts, _, rolloutSvc := newUpdatesConsole(t)
	if code := postForm(t, ts, "/org/updates/policy", url.Values{
		"testgroup": {"test"}, "percents": {"50, 50"},
	}); code != 303 {
		t.Fatalf("derive = %d", code)
	}
	if code := postForm(t, ts, "/rollout", url.Values{
		"target": {"deadbeef"}, "scope": {"*"}, "confirmed": {"1"},
	}); code != 303 {
		t.Fatalf("start = %d", code)
	}
	// A builder that never finishes puts the engine in the await-build
	// phase on the next tick (build-before-promote, real flow).
	rolloutSvc.WithCacheBuilder(stubCacheBuilder{})
	if code := postForm(t, ts, "/rollout/tick", url.Values{}); code != 303 {
		t.Fatalf("tick = %d", code)
	}
	_, page := getPage(t, ts, "/updates/rollout")
	if !strings.Contains(page, "Building release") {
		t.Fatalf("building phase not shown: %.400s", page)
	}
}
