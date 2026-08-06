package main

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// gitIn runs git in dir with a fixed identity, so the test does not depend on
// whatever the machine has configured.
func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
}

// stageRefRepo builds an origin holding fleet.json plus a flake.lock, a change
// branch that edits ONLY the lock, and a runner clone tracking main.
func stageRefRepo(t *testing.T) (*server, string) {
	t.Helper()
	root := t.TempDir()
	origin := filepath.Join(root, "origin")
	work := filepath.Join(root, "work", "overlay")
	if err := os.MkdirAll(origin, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "init", "-q", "-b", "main")
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(origin, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("fleet.json", baseFleet)
	write("flake.lock", `{"core":"OLD"}`)
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-q", "-m", "seed")

	// The change: a new core, and nothing else. This is exactly the shape that
	// got through the gate on 2026-08-06 - fleet.json is untouched, so the old
	// protocol carried nothing that could reveal it.
	gitIn(t, origin, "checkout", "-q", "-b", "cr/core-bump")
	write("flake.lock", `{"core":"NEW"}`)
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-q", "-m", "core: bump")
	gitIn(t, origin, "checkout", "-q", "main")

	if err := os.MkdirAll(filepath.Dir(work), 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, filepath.Dir(work), "clone", "-q", origin, "overlay")
	return &server{log: discardLog(), workdir: work, branch: "main"}, origin
}

// TestStageCandidateSeesAChangeThatOnlyTouchesTheLock is the test ADR 0020
// requires. A DAWO core update changes flake.lock and nothing else; under the
// old protocol only fleet.json travelled, so the runner evaluated the update
// against the core it was replacing, said yes truthfully, and the merge broke
// main for the workplace class.
func TestStageCandidateSeesAChangeThatOnlyTouchesTheLock(t *testing.T) {
	s, _ := stageRefRepo(t)
	ctx := context.Background()

	// Without a ref: the old behaviour, and the old blind spot.
	scratch, err := s.stageCandidate(ctx, baseFleet, "")
	if err != nil {
		t.Fatal(err)
	}
	if got := readFile(t, filepath.Join(scratch, "flake.lock")); got != `{"core":"OLD"}` {
		t.Fatalf("without a ref the runner should see main's lock, got %s", got)
	}

	// With the ref: the change's own lock is what gets evaluated.
	scratch, err = s.stageCandidate(ctx, baseFleet, "cr/core-bump")
	if err != nil {
		t.Fatalf("staging the change branch failed: %v", err)
	}
	if got := readFile(t, filepath.Join(scratch, "flake.lock")); got != `{"core":"NEW"}` {
		t.Fatalf("the gate is still evaluating the core being replaced, got %s", got)
	}
	// The candidate settings still win over whatever the branch carries: the
	// operator's unsaved edit is the thing under test.
	if got := readFile(t, filepath.Join(scratch, "fleet.json")); got != baseFleet {
		t.Fatalf("candidate fleet.json did not survive the merge: %s", got)
	}
}

// TestStageCandidateReportsAMergeConflictAsAVerdict: a change that will not
// merge is the approver's problem, not the runner's. It has to be
// distinguishable, because one becomes a 422 the console renders as a
// rejection and the other a 500 somebody gets paged for.
func TestStageCandidateReportsAMergeConflictAsAVerdict(t *testing.T) {
	s, origin := stageRefRepo(t)
	ctx := context.Background()

	// Move main so the branch's lock edit collides.
	gitIn(t, origin, "checkout", "-q", "main")
	if err := os.WriteFile(filepath.Join(origin, "flake.lock"), []byte(`{"core":"SIDEWAYS"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, origin, "add", ".")
	gitIn(t, origin, "commit", "-q", "-m", "core: a different bump")
	// handleValidate syncs before staging; without it the clone is still on
	// the old main and the branch merges cleanly, which would make this test
	// pass for the wrong reason.
	if err := s.sync(ctx); err != nil {
		t.Fatal(err)
	}

	_, err := s.stageCandidate(ctx, baseFleet, "cr/core-bump")
	if err == nil {
		t.Fatal("a conflicting change staged cleanly")
	}
	var mc *mergeConflict
	if !errors.As(err, &mc) {
		t.Fatalf("conflict reported as an infrastructure failure, not a verdict: %v", err)
	}

	// And the worktree is usable again: it is reused across requests, so a
	// half-merged tree would poison every later validation.
	if _, err := s.stageCandidate(ctx, baseFleet, ""); err != nil {
		t.Fatalf("the worktree was left half-merged: %v", err)
	}
}

// TestStageCandidateRefusesAnOddRef: the console is authenticated, which is not
// the same as being allowed to name anything. The ref reaches git's argv.
func TestStageCandidateRefusesAnOddRef(t *testing.T) {
	s, _ := stageRefRepo(t)
	for _, bad := range []string{"--upload-pack=evil", "../../etc", "cr/../main", "-x"} {
		if _, err := s.stageCandidate(context.Background(), baseFleet, bad); err == nil {
			t.Errorf("ref %q was accepted", bad)
		}
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
