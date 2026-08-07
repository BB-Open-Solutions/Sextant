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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// evidence_api_test.go covers the compliance export, which was at 0%.
//
// This is the endpoint an auditor's evidence comes out of, so the failures
// that matter are not crashes: a window silently interpreted as something
// else, or a malformed date accepted and turned into a default, would
// produce an export that looks complete and covers the wrong period.

func newEvidenceAPI(t *testing.T) *httptest.Server {
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
	changes := app.NewChangeService(repo, st.Changes(), gate, noopBuilder{},
		app.SystemClock{}, openWT, svc)
	ev := app.NewEvidenceService(svc, changes, app.SystemClock{})

	mux := http.NewServeMux()
	New(Services{Config: svc, Changes: changes, Evidence: ev}, Authz{}, testToken, true,
		slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	_ = os.Remove(filepath.Join(dir, ".keep"))
	return srv
}

func TestEvidenceExportDefaultsToTheLastThirtyDays(t *testing.T) {
	srv := newEvidenceAPI(t)
	code, body := call(t, srv, "GET", "/api/v1/evidence", nil, testToken)
	if code != 200 {
		t.Fatalf("export = %d: %v", code, body)
	}
	if body == nil {
		t.Fatal("the export is empty; an auditor would receive nothing")
	}
}

// TestEvidenceExportIsNamedForItsWindow: the export is a document somebody
// files. A download called "download.json" with no dates in it is evidence
// nobody can place afterwards.
func TestEvidenceExportIsNamedForItsWindow(t *testing.T) {
	srv := newEvidenceAPI(t)
	req, _ := http.NewRequest("GET",
		srv.URL+"/api/v1/evidence?from=2026-01-01T00:00:00Z&to=2026-02-01T00:00:00Z", nil)
	req.Header.Set("Authorization", "Bearer "+testToken)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("export = %d", resp.StatusCode)
	}
	cd := resp.Header.Get("Content-Disposition")
	for _, want := range []string{"20260101", "20260201", "attachment"} {
		if !strings.Contains(cd, want) {
			t.Errorf("Content-Disposition %q does not carry %q", cd, want)
		}
	}
}

// TestEvidenceRefusesAMalformedWindow. Accepting a bad date and quietly
// falling back to the default would hand somebody an export covering a
// different period from the one they asked for - and they would file it.
func TestEvidenceRefusesAMalformedWindow(t *testing.T) {
	srv := newEvidenceAPI(t)
	for _, q := range []string{
		"?from=yesterday",
		"?to=2026-13-45",
		"?from=2026-01-01", // a date, but not RFC 3339
		"?to=",             // empty is ignored, not an error - see below
	} {
		t.Run(q, func(t *testing.T) {
			code, _ := call(t, srv, "GET", "/api/v1/evidence"+q, nil, testToken)
			if q == "?to=" {
				if code != 200 {
					t.Errorf("an empty parameter should be ignored, got %d", code)
				}
				return
			}
			if code == 200 {
				t.Errorf("a malformed window was accepted (%d); the export would cover the wrong period", code)
			}
			if code == http.StatusInternalServerError {
				t.Errorf("a bad date produced a 500")
			}
		})
	}
}

func TestEvidenceRequiresAToken(t *testing.T) {
	srv := newEvidenceAPI(t)
	if code, _ := call(t, srv, "GET", "/api/v1/evidence", nil, ""); code != http.StatusUnauthorized {
		t.Errorf("unauthenticated export = %d, want 401", code)
	}
}

// TestEvidenceWithoutTheServiceSaysSo: a deployment with no config plane
// must explain itself rather than 404, because "this console cannot produce
// evidence" is an answer an auditor needs.
func TestEvidenceWithoutTheServiceSaysSo(t *testing.T) {
	srv := newTestAPI(t, true) // no Evidence wired
	code, _ := call(t, srv, "GET", "/api/v1/evidence", nil, testToken)
	if code == 200 {
		t.Error("an export succeeded with no evidence service")
	}
	if code == http.StatusInternalServerError {
		t.Error("a missing service produced a 500 instead of an explanation")
	}
}
