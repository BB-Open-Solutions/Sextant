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
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// settings_stage_test.go covers what happens when an organisation requires
// every change to go through review: saving the settings page does not fail,
// it flows into the review process.
//
// That is a deliberate product decision and it was at 0%. The failure it
// guards against is the obvious alternative - a save that errors - which
// would teach operators that the governance setting is broken and should be
// turned off.

const reviewedSeed = `{
  "version": 3,
  "org": {"settings": {}},
  "assurance": {"requireChangeRequest": true},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hp-g4", "class": "laptop"}}
}`

func newReviewedConsole(t *testing.T) (*httptest.Server, *app.ChangeService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{
		"fleet.json": reviewedSeed, "catalog.json": seedCatalog,
		"profiles.json": seedProfiles, "bundles.json": seedBundles,
	} {
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
	gate := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	cfg, err := app.NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	openWT := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	changes := app.NewChangeService(repo, st.Changes(), gate, stubBuilder{},
		&stubClock{time.Now()}, openWT, cfg)
	srv, err := web.New(web.Services{Config: cfg, Changes: changes},
		web.DevSessions{}, true, nil, nil, nil, log)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, changes
}

// TestSavingUnderReviewStagesAChangeInsteadOfFailing is the behaviour.
func TestSavingUnderReviewStagesAChangeInsteadOfFailing(t *testing.T) {
	ts, changes := newReviewedConsole(t)
	ctx := context.Background()

	before, err := changes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}

	resp, err := client().PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "v:apps.office": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	// A redirect, not an error: a review-gated org does not fail the save.
	if resp.StatusCode != 303 {
		t.Fatalf("save under review = %d, want a redirect into the review flow", resp.StatusCode)
	}

	after, err := changes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before)+1 {
		t.Fatalf("changes went from %d to %d; the save did not stage one", len(before), len(after))
	}
	// The staged change has to carry the edit, or the operator is sent to a
	// review board holding an empty draft.
	var id string
	for _, cr := range after {
		id = cr.ID
	}
	diff, err := changes.Diff(ctx, id)
	if err != nil {
		t.Fatalf("diff: %v", err)
	}
	if diff == "" {
		t.Error("the staged change has no diff; a reviewer would approve nothing")
	}
}

// TestSavingAValueMainAlreadyHoldsLeavesNoEmptyDraft covers the guard in
// stageSettingsAsChange: a save that computes no change must not leave a
// draft change request on the board for somebody to triage and abandon.
//
// The value has to be MERGED first. Two saves before a merge legitimately
// produce two change requests - main does not hold the value yet, so the
// second save is a real edit. That was my first version of this test, and
// the code was right where the test was wrong.
func TestSavingAValueMainAlreadyHoldsLeavesNoEmptyDraft(t *testing.T) {
	ts, changes := newReviewedConsole(t)
	ctx := context.Background()

	resp, err := client().PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "v:apps.office": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	list, err := changes.List(ctx)
	if err != nil || len(list) != 1 {
		t.Fatalf("expected one staged change, got %d (%v)", len(list), err)
	}
	id := list[0].ID
	if _, err := changes.Submit(ctx, id); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := changes.Merge(ctx, id, ports.Author{Name: "ada"}); err != nil {
		t.Fatalf("merge: %v", err)
	}

	// Now main holds it, so submitting the same value computes nothing.
	if resp, err = client().PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "v:apps.office": {"true"},
	}); err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	after, err := changes.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, cr := range after {
		if cr.ID != id && cr.Status != "abandoned" && cr.Status != "merged" {
			t.Errorf("a no-op save left change %q open with status %q", cr.ID, cr.Status)
		}
	}
}
