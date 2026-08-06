package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// stageRepo builds a bare-ish origin plus a clone the runner treats as its
// workdir, seeded with one fleet.json on the given branch.
func stageRepo(t *testing.T, fleetDoc string) *server {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	work := filepath.Join(root, "work", "overlay")

	run := func(dir string, args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
		}
	}
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	run(origin, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(origin, "fleet.json"), []byte(fleetDoc), 0o644); err != nil {
		t.Fatal(err)
	}
	run(origin, "add", ".")
	run(origin, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	if err := os.MkdirAll(filepath.Dir(work), 0o755); err != nil {
		t.Fatal(err)
	}
	run(filepath.Dir(work), "clone", "-q", origin, "overlay")

	return &server{
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		workdir: work,
		branch:  "main",
	}
}

const baseFleet = `{"version":3,"org":{"settings":{}},"devices":{}}`

// TestStageCandidateAcceptsAnUnchangedFleet: found on the production console,
// 2026-08-05. Both DAWO core updates in the review queue showed
//
//	gate-runner error (status 500): {"ok":false,"error":"staging candidate failed"}
//
// and the runner's own log carried the real cause: "nothing to commit, working
// tree clean". A core update moves the flake's core pin and leaves fleet.json
// untouched, so its candidate is byte-identical to the base and `git commit`
// exits 1. Every core update failed the gate for as long as this existed.
//
// An unchanged candidate is a valid request. The gate's job is to answer
// whether the configuration evaluates, and "the same as what already
// evaluates" is a fine thing to be asked about.
func TestStageCandidateAcceptsAnUnchangedFleet(t *testing.T) {
	s := stageRepo(t, baseFleet)
	scratch, err := s.stageCandidate(context.Background(), baseFleet, "")
	if err != nil {
		t.Fatalf("staging an unchanged candidate failed: %v", err)
	}
	// The eval needs a clean tree: a dirty flake is copied to the store whole
	// on every eval and loses its eval cache. So the empty commit must still
	// have happened.
	cmd := exec.Command("git", "status", "--porcelain")
	cmd.Dir = scratch
	out, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 0 {
		t.Fatalf("scratch worktree is dirty after staging:\n%s", out)
	}
}

// TestStageCandidateWritesTheCandidate: the ordinary path still works - a
// changed candidate lands in the scratch tree and is what gets evaluated,
// not the base it was staged from.
func TestStageCandidateWritesTheCandidate(t *testing.T) {
	s := stageRepo(t, baseFleet)
	changed := `{"version":3,"org":{"settings":{"dawo.identity.enable":true}},"devices":{}}`
	scratch, err := s.stageCandidate(context.Background(), changed, "")
	if err != nil {
		t.Fatalf("stageCandidate: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(scratch, "fleet.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != changed {
		t.Fatalf("scratch fleet.json is not the candidate:\n%s", got)
	}
}

// TestStageCandidateIsReusable: the worktree is reused across calls, so a
// second validation must not inherit the first one's candidate. This is the
// path that runs in production - the scratch tree exists after the first
// request and is checked out again rather than created.
func TestStageCandidateIsReusable(t *testing.T) {
	s := stageRepo(t, baseFleet)
	first := `{"version":3,"org":{"settings":{"a":1}},"devices":{}}`
	if _, err := s.stageCandidate(context.Background(), first, ""); err != nil {
		t.Fatalf("first stage: %v", err)
	}
	// Second call: unchanged relative to the BASE, but different from what the
	// worktree currently holds. Both properties matter.
	scratch, err := s.stageCandidate(context.Background(), baseFleet, "")
	if err != nil {
		t.Fatalf("second stage: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(scratch, "fleet.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != baseFleet {
		t.Fatalf("second candidate did not replace the first:\n%s", got)
	}
}

// failingGit is a server whose git calls fail on the named subcommand, so a
// handler's error path can be exercised without a real repository.
func serverFailingOn(t *testing.T, sub string) *server {
	t.Helper()
	root := t.TempDir()
	// scratch is sibling to workdir; create it so the candidate write lands
	// somewhere even though every git call is stubbed.
	if err := os.MkdirAll(filepath.Join(root, "validate"), 0o755); err != nil {
		t.Fatal(err)
	}
	return &server{
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		workdir: filepath.Join(root, "overlay"),
		branch:  "main",
		sem:     make(chan struct{}, 1),
		gitRun: func(_ context.Context, _ string, args ...string) error {
			for _, a := range args {
				if a == sub {
					return fmt.Errorf("git %v: exit status 1: %s failed for a reason worth reading", args, sub)
				}
			}
			return nil
		},
	}
}

func postValidate(t *testing.T, s *server) *httptest.ResponseRecorder {
	t.Helper()
	body := `{"fleet":"{\"version\":3}","hosts":["lt-1"]}`
	req := httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader(body))
	rec := httptest.NewRecorder()
	s.handleValidate(rec, req)
	return rec
}

// TestValidateStagingFailureCarriesItsCause: an operator reading the console
// must get the sentence the runner already wrote to its log. Before this, the
// response was a fixed string and the cause lived only in `kubectl logs`.
func TestValidateStagingFailureCarriesItsCause(t *testing.T) {
	rec := postValidate(t, serverFailingOn(t, "commit"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body.String())
	}
	var vr validateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &vr); err != nil {
		t.Fatal(err)
	}
	if vr.Error != "staging candidate failed" {
		t.Fatalf("classification changed: %q", vr.Error)
	}
	if !strings.Contains(vr.Detail, "commit failed for a reason worth reading") {
		t.Fatalf("cause did not travel: %q", vr.Detail)
	}
}

// TestValidateSyncFailureStaysOpaque: the other half of the same decision.
// Sync talks to the private overlay remote, and git names that host and path
// when it fails, so this one keeps its cause to the pod log on purpose.
func TestValidateSyncFailureStaysOpaque(t *testing.T) {
	rec := postValidate(t, serverFailingOn(t, "fetch"))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d\n%s", rec.Code, rec.Body.String())
	}
	var vr validateResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &vr); err != nil {
		t.Fatal(err)
	}
	if vr.Error != "overlay sync failed" {
		t.Fatalf("classification = %q", vr.Error)
	}
	if vr.Detail != "" {
		t.Fatalf("sync detail leaked to the caller: %q", vr.Detail)
	}
}

// TestShortDetailIsBounded: a runaway error must not become the page.
func TestShortDetailIsBounded(t *testing.T) {
	got := shortDetail(fmt.Errorf("%s", strings.Repeat("x", maxDetail*3)))
	if len(got) > maxDetail+3 {
		t.Fatalf("detail not bounded: %d chars", len(got))
	}
	if !strings.HasPrefix(got, "...") {
		t.Fatal("a trimmed detail should say it was trimmed")
	}
}
