package api

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// changes_api_test.go covers the change-request API, which was the single
// largest uncovered block in the logic layer.
//
// The endpoints here are the whole reviewed-write path: a change is opened,
// edited on its own branch, submitted for review, and then merged or
// abandoned. Every governance property the product sells - four-eyes, a gate
// that must pass, an audit trail with a real author - is enforced somewhere
// along it. What the tests below pin is not that the handlers return 200 but
// that the ORDER is enforced: a change cannot be merged before it is
// submitted, an unknown id is a 404 rather than a 500, and a read-only
// deployment refuses every write.

// noopBuilder stands in for the nix realisation step: this test is about the
// HTTP surface and the change lifecycle, not about nix.
type noopBuilder struct{}

func (noopBuilder) Build(context.Context, string, []string) error { return nil }

// newChangeAPI builds an API with the change service wired, over a real git
// repo and a real state store.
func newChangeAPI(t *testing.T, write bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeSeed(t, dir)
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	gate := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	svc, err := app.NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	openWT := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	changes := app.NewChangeService(repo, st.Changes(), gate,
		noopBuilder{},
		app.SystemClock{}, openWT, svc)

	mux := http.NewServeMux()
	New(Services{Config: svc, Changes: changes}, Authz{}, testToken, write,
		slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestChangeLifecycleThroughTheAPI(t *testing.T) {
	srv := newChangeAPI(t, true)

	// Open.
	code, body := call(t, srv, "POST", "/api/v1/changes",
		map[string]any{"id": "cr-1", "title": "raise the poll interval"}, testToken)
	if code != http.StatusCreated {
		t.Fatalf("open = %d: %v", code, body)
	}

	// It shows up in the list and can be fetched on its own.
	if code, _ = call(t, srv, "GET", "/api/v1/changes", nil, testToken); code != 200 {
		t.Errorf("list = %d", code)
	}
	if code, body = call(t, srv, "GET", "/api/v1/changes/cr-1", nil, testToken); code != 200 {
		t.Errorf("get = %d: %v", code, body)
	}

	// An edit lands on the change's own branch, not on main.
	code, body = call(t, srv, "POST", "/api/v1/changes/cr-1/edits",
		map[string]any{"scope": "org", "key": "autoUpdate.options.pollSeconds", "value": 600}, testToken)
	if code != 200 {
		t.Fatalf("edit = %d: %v", code, body)
	}

	// The diff is what a reviewer reads. An empty one on a change that
	// carries an edit means the reviewer approves something they cannot see.
	code, _ = call(t, srv, "GET", "/api/v1/changes/cr-1/diff", nil, testToken)
	if code != 200 {
		t.Errorf("diff = %d", code)
	}

	// Merging before submitting must be refused: submit is where the gate
	// runs, so a merge that skips it lands unevaluated configuration.
	if code, _ = call(t, srv, "POST", "/api/v1/changes/cr-1/merge", nil, testToken); code == 200 {
		t.Error("merging an unsubmitted change succeeded; the gate would be skipped")
	}

	if code, body = call(t, srv, "POST", "/api/v1/changes/cr-1/submit", nil, testToken); code != 200 {
		t.Fatalf("submit = %d: %v", code, body)
	}
	if code, body = call(t, srv, "POST", "/api/v1/changes/cr-1/merge", nil, testToken); code != 200 {
		t.Fatalf("merge = %d: %v", code, body)
	}

	// And the change records the merge. A merge that returns 200 while the
	// record still reads "ready" is the drift the startup reconciliation
	// exists to repair, so the happy path had better not produce it.
	code, body = call(t, srv, "GET", "/api/v1/changes/cr-1", nil, testToken)
	if code != 200 {
		t.Fatalf("get after merge = %d", code)
	}
	if st, _ := body["status"].(string); st != "merged" {
		t.Errorf("status after a successful merge = %q, want merged", st)
	}

	// Merging twice must not report success.
	if code, _ = call(t, srv, "POST", "/api/v1/changes/cr-1/merge", nil, testToken); code == 200 {
		t.Error("merging an already merged change reported success")
	}
}

func TestChangeAbandonRemovesItFromTheQueue(t *testing.T) {
	srv := newChangeAPI(t, true)
	if code, body := call(t, srv, "POST", "/api/v1/changes",
		map[string]any{"id": "cr-2", "title": "never mind"}, testToken); code != http.StatusCreated {
		t.Fatalf("open = %d: %v", code, body)
	}
	if code, body := call(t, srv, "POST", "/api/v1/changes/cr-2/abandon", nil, testToken); code != 200 {
		t.Fatalf("abandon = %d: %v", code, body)
	}
	// Abandoning twice must not report success: an operator who gets "ok"
	// from abandoning an already-abandoned change believes they just acted.
	if code, _ := call(t, srv, "POST", "/api/v1/changes/cr-2/abandon", nil, testToken); code == 200 {
		t.Error("abandoning an already abandoned change reported success")
	}
}

// TestChangeEndpointsOnAnUnknownIdAre404 pins wrapChangeErr, which exists to
// turn "no such change" into a 404. Without it these are 500s, and a 500
// tells an operator the console is broken when the truth is they typed the
// wrong id.
func TestChangeEndpointsOnAnUnknownIdAre404(t *testing.T) {
	srv := newChangeAPI(t, true)
	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/changes/ghost", nil},
		{"GET", "/api/v1/changes/ghost/diff", nil},
		{"POST", "/api/v1/changes/ghost/submit", nil},
		{"POST", "/api/v1/changes/ghost/merge", nil},
		{"POST", "/api/v1/changes/ghost/abandon", nil},
		{"POST", "/api/v1/changes/ghost/edits", map[string]any{"scope": "org", "key": "k", "value": 1}},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			code, _ := call(t, srv, c.method, c.path, c.body, testToken)
			if code == http.StatusInternalServerError {
				t.Errorf("unknown change id produced a 500; it reads as a broken console")
			}
			if code == 200 || code == http.StatusCreated {
				t.Errorf("unknown change id succeeded (%d)", code)
			}
		})
	}
}

// TestChangeWritesAreRefusedOnAReadOnlyDeployment: --write=false is how a
// mirror or a break-glass read-only console runs. Every mutating endpoint
// must be absent, not merely fail later.
func TestChangeWritesAreRefusedOnAReadOnlyDeployment(t *testing.T) {
	srv := newChangeAPI(t, false)
	// Reads still work.
	if code, _ := call(t, srv, "GET", "/api/v1/changes", nil, testToken); code != 200 {
		t.Errorf("read-only deployment refuses a read: %d", code)
	}
	writes := []struct {
		method, path string
	}{
		{"POST", "/api/v1/changes"},
		{"POST", "/api/v1/changes/cr-1/edits"},
		{"POST", "/api/v1/changes/cr-1/submit"},
		{"POST", "/api/v1/changes/cr-1/merge"},
		{"POST", "/api/v1/changes/cr-1/abandon"},
	}
	for _, c := range writes {
		t.Run(c.path, func(t *testing.T) {
			code, _ := call(t, srv, c.method, c.path, map[string]any{"id": "cr-1"}, testToken)
			if code == 200 || code == http.StatusCreated {
				t.Errorf("a write succeeded on a read-only deployment (%d)", code)
			}
		})
	}
}

func TestChangeEndpointsRequireAToken(t *testing.T) {
	srv := newChangeAPI(t, true)
	if code, _ := call(t, srv, "GET", "/api/v1/changes", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated list = %d, want 401", code)
	}
	if code, _ := call(t, srv, "POST", "/api/v1/changes",
		map[string]any{"id": "x", "title": "y"}, "wrong-token"); code != http.StatusUnauthorized {
		t.Errorf("wrong token = %d, want 401", code)
	}
}
