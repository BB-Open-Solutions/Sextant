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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// stubBuilder never actually builds; Submit only calls the builder for a
// change scoped to specific hosts, so a no-op is enough to walk a scoped
// change through to Ready in a test.
type stubBuilder struct{}

func (stubBuilder) Build(context.Context, string, []string) error { return nil }

// newChangeConsole wires the console with a real, fully lifecycle-capable
// ChangeService (Open/Edit/Submit/Merge) over the ladder fleet (see
// rollout_updates_test.go), so the merge confirmation handler (fix B) can be
// driven end to end: open, edit, submit, then merge through the HTTP layer.
func newChangeConsole(t *testing.T) (*httptest.Server, *app.ConfigService, *app.ChangeService) {
	t.Helper()
	return newChangeConsoleWithGate(t, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
}

// newChangeConsoleWithGate is newChangeConsole with a caller-chosen gate for
// the CHANGE flow, so a test can watch what a rejection does to the pages
// that render it. The config service keeps its own allow-gate: the seed has
// to be loadable, or the test proves nothing about the rejection.
func newChangeConsoleWithGate(t *testing.T, changeGate ports.Gate) (*httptest.Server, *app.ConfigService, *app.ChangeService) {
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
	openWT := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	changeSvc := app.NewChangeService(repo, st.Changes(), changeGate,
		stubBuilder{}, clock, openWT, cfg)
	rolloutSvc := app.NewRolloutService(cfg, st.Rollouts(), &stubConvergence{}, clock, log)
	srv, err := web.New(web.Services{Config: cfg, Changes: changeSvc, Rollouts: rolloutSvc}, web.DevSessions{}, true,
		nil, nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg, changeSvc
}

// postFormBody is postForm plus the response body, for tests that must read
// the rendered confirmation page.
func postFormBody(t *testing.T, ts *httptest.Server, path string, form url.Values) (int, string) {
	t.Helper()
	form.Set("csrf", "dev-csrf")
	resp, err := client().PostForm(ts.URL+path, form)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(body)
}

func TestChangeMergeRequiresConfirmation(t *testing.T) {
	ts, _, changeSvc := newChangeConsole(t)
	ctx := context.Background()
	author := ports.Author{Name: "ada", Subject: "ada", Email: "ada@example.com"}

	if _, err := changeSvc.Open(ctx, "cr-1", "Turn on office suite for mid", author); err != nil {
		t.Fatal(err)
	}
	// Scoped to the "mid" group's devices - not the whole fleet - so the
	// confirmation page's scope line has something specific to say.
	if err := changeSvc.Edit(ctx, "cr-1", fleet.SetScopeSetting("group:mid", "apps.office", true),
		"edit", author, "mi-1", "mi-2", "mi-3"); err != nil {
		t.Fatal(err)
	}
	if cr, err := changeSvc.Submit(ctx, "cr-1"); err != nil || cr.Status != change.Ready {
		t.Fatalf("submit: %+v, %v", cr, err)
	}

	// The bare POST (no confirmed=1) renders a confirmation and does NOT merge.
	code, page := postFormBody(t, ts, "/changes/cr-1/merge", url.Values{})
	if code != 200 {
		t.Fatalf("unconfirmed merge = %d", code)
	}
	for _, want := range []string{"Turn on office suite for mid", "mid", "Confirm merge"} {
		if !strings.Contains(page, want) {
			t.Errorf("confirm page missing %q", want)
		}
	}
	if cr, _, err := changeSvc.Get(ctx, "cr-1"); err != nil || cr.Status != change.Ready {
		t.Fatalf("unconfirmed POST must not merge: %+v (%v)", cr, err)
	}

	// confirmed=1 actually merges.
	if code := postForm(t, ts, "/changes/cr-1/merge", url.Values{"confirmed": {"1"}}); code != 303 {
		t.Fatalf("confirmed merge = %d", code)
	}
	if cr, _, err := changeSvc.Get(ctx, "cr-1"); err != nil || cr.Status != change.Merged {
		t.Fatalf("confirmed POST must merge: %+v (%v)", cr, err)
	}
}

func TestChangeMergeConfirmationShowsFleetWideScope(t *testing.T) {
	ts, _, changeSvc := newChangeConsole(t)
	ctx := context.Background()
	author := ports.Author{Name: "ada", Subject: "ada", Email: "ada@example.com"}

	if _, err := changeSvc.Open(ctx, "cr-2", "Org-wide setting", author); err != nil {
		t.Fatal(err)
	}
	// An org-scoped edit records no hosts, so RecordHosts marks the change
	// whole-fleet (see domain/change.CR.RecordHosts).
	if err := changeSvc.Edit(ctx, "cr-2", fleet.SetScopeSetting("org", "desktop", "gnome"),
		"edit", author); err != nil {
		t.Fatal(err)
	}
	if cr, err := changeSvc.Submit(ctx, "cr-2"); err != nil || cr.Status != change.Ready {
		t.Fatalf("submit: %+v, %v", cr, err)
	}

	code, page := postFormBody(t, ts, "/changes/cr-2/merge", url.Values{})
	if code != 200 {
		t.Fatalf("unconfirmed merge = %d", code)
	}
	if !strings.Contains(page, "Whole fleet") {
		t.Errorf("whole-fleet change did not show fleet-wide scope: %s", page)
	}
}

// TestChangeMergeFourEyesUnchangedByConfirmation proves fix B kept the
// four-eyes / author segregation-of-duties check exactly where it was: the
// unconfirmed preview never enforces it (it does not merge), but the
// confirmed POST that actually merges still refuses a change approved by its
// own author.
func TestChangeMergeFourEyesUnchangedByConfirmation(t *testing.T) {
	ts, cfg, changeSvc := newChangeConsole(t)
	ctx := context.Background()
	// DevSessions always authenticates as subject "dev"; open the change as
	// that same subject so approving it collides with authorship.
	author := ports.Author{Name: "dev", Subject: "dev", Email: "dev@localhost"}

	if _, err := changeSvc.Open(ctx, "cr-3", "Self-approved change", author); err != nil {
		t.Fatal(err)
	}
	if err := changeSvc.Edit(ctx, "cr-3", fleet.SetScopeSetting("org", "desktop", "gnome"),
		"edit", author); err != nil {
		t.Fatal(err)
	}
	if cr, err := changeSvc.Submit(ctx, "cr-3"); err != nil || cr.Status != change.Ready {
		t.Fatalf("submit: %+v, %v", cr, err)
	}
	if err := cfg.ApplyStructural(ctx, fleet.SetAssurance(fleet.Assurance{RequireFourEyes: true}),
		"assurance: four-eyes=true", author); err != nil {
		t.Fatal(err)
	}

	// The preview still renders even though this merge will fail four-eyes -
	// the check only runs on the POST that actually merges.
	if code, _ := postFormBody(t, ts, "/changes/cr-3/merge", url.Values{}); code != 200 {
		t.Fatalf("unconfirmed preview = %d, want 200 even under four-eyes", code)
	}
	// Confirming hits the same four-eyes rule Changes.Merge always enforced.
	if code := postForm(t, ts, "/changes/cr-3/merge", url.Values{"confirmed": {"1"}}); code != 400 {
		t.Fatalf("confirmed self-merge under four-eyes = %d, want 400", code)
	}
	if cr, _, err := changeSvc.Get(ctx, "cr-3"); err != nil || cr.Status != change.Ready {
		t.Fatalf("four-eyes rejection must not merge: %+v (%v)", cr, err)
	}
}
