package main

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// clone_test.go covers the two functions that decide whether the runner
// trusts its own working copy. Both were at 0%.
//
// They matter more than their size suggests. cloneUsable IS /readyz: if it
// answers "fine" for a directory git will not touch, the runner reports
// ready and then fails every /validate - and because the gate is fail-closed,
// every path that commits configuration stops with it. That is not a
// hypothetical shape; a five-device fleet froze this control plane for an
// hour on 2026-08-01 when the runner went down.
//
// Tested against real git rather than the gitRun seam, because what is being
// asserted here is what git thinks of a directory - and a fake would only
// assert what we already believe.

func gitOrSkip(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
}

func run(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// originRepo builds a bare-ish source repo the runner can clone from.
func originRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(`{"version":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", ".")
	run(t, dir, "commit", "-q", "-m", "seed")
	return dir
}

func testServer(t *testing.T, workdir, remote string) *server {
	t.Helper()
	return &server{
		log:     slog.New(slog.NewTextHandler(io.Discard, nil)),
		workdir: workdir,
		remote:  remote,
		branch:  "main",
	}
}

func TestEnsureCloneCreatesTheWorkingCopy(t *testing.T) {
	gitOrSkip(t)
	origin := originRepo(t)
	work := filepath.Join(t.TempDir(), "nested", "overlay")

	s := testServer(t, work, origin)
	if err := s.ensureClone(context.Background()); err != nil {
		t.Fatalf("ensureClone: %v", err)
	}
	// The parent directory did not exist either: a fresh PVC is empty, not
	// pre-shaped, and a runner that needs somebody to mkdir first is a runner
	// that fails its first start after a volume is replaced.
	if _, err := os.Stat(filepath.Join(work, "fleet.json")); err != nil {
		t.Fatalf("clone did not produce a tree: %v", err)
	}
	if err := s.cloneUsable(context.Background()); err != nil {
		t.Errorf("cloneUsable says no right after a successful clone: %v", err)
	}
}

// TestEnsureCloneOnAnExistingCloneSyncsInsteadOfRecloning: the workdir is a
// PVC that survives restarts. Re-cloning each time would be slow and would
// throw away the local object store; not syncing would evaluate a stale tree
// and answer a question about a revision nobody asked about.
func TestEnsureCloneOnAnExistingCloneSyncsInsteadOfRecloning(t *testing.T) {
	gitOrSkip(t)
	origin := originRepo(t)
	work := filepath.Join(t.TempDir(), "overlay")
	s := testServer(t, work, origin)
	if err := s.ensureClone(context.Background()); err != nil {
		t.Fatal(err)
	}

	// A new commit upstream, and local dirt that must not survive: sync hard
	// resets so evaluation starts from the tracked head, not from whatever a
	// previous /validate left behind.
	if err := os.WriteFile(filepath.Join(origin, "new.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, origin, "add", ".")
	run(t, origin, "commit", "-q", "-m", "second")
	if err := os.WriteFile(filepath.Join(work, "fleet.json"), []byte("LEFTOVER"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := s.ensureClone(context.Background()); err != nil {
		t.Fatalf("second ensureClone: %v", err)
	}
	if _, err := os.Stat(filepath.Join(work, "new.txt")); err != nil {
		t.Errorf("the upstream commit did not arrive: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(work, "fleet.json"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) == "LEFTOVER" {
		t.Error("a leftover edit survived the sync; evaluation would run against a dirty tree")
	}
}

// TestCloneUsableRefusesWhatGitRefuses is the /readyz contract. Each case is
// a state a PVC can genuinely be in.
func TestCloneUsableRefusesWhatGitRefuses(t *testing.T) {
	gitOrSkip(t)
	ctx := context.Background()

	t.Run("directory does not exist", func(t *testing.T) {
		s := testServer(t, filepath.Join(t.TempDir(), "nope"), "")
		if err := s.cloneUsable(ctx); err == nil {
			t.Error("ready with no working copy at all")
		}
	})

	t.Run("directory exists but is not a repo", func(t *testing.T) {
		// A fresh volume mounted where a clone used to be: present, empty,
		// and useless. This is the case that made the check worth having.
		dir := t.TempDir()
		s := testServer(t, dir, "")
		if err := s.cloneUsable(ctx); err == nil {
			t.Error("ready for a directory that is not a git working tree")
		}
	})

	t.Run("repo with a corrupted .git", func(t *testing.T) {
		origin := originRepo(t)
		work := filepath.Join(t.TempDir(), "overlay")
		s := testServer(t, work, origin)
		if err := s.ensureClone(ctx); err != nil {
			t.Fatal(err)
		}
		if err := os.Remove(filepath.Join(work, ".git", "HEAD")); err != nil {
			t.Fatal(err)
		}
		if err := s.cloneUsable(ctx); err == nil {
			t.Error("ready for a repo git cannot read")
		}
	})
}

// TestEnsureCloneSurfacesAnUnreachableRemote: a clone that cannot happen has
// to be an error the caller sees. Silently continuing would leave the runner
// answering /validate against an empty directory, which evaluates to
// "everything is fine" for a fleet it never read.
func TestEnsureCloneSurfacesAnUnreachableRemote(t *testing.T) {
	gitOrSkip(t)
	work := filepath.Join(t.TempDir(), "overlay")
	s := testServer(t, work, filepath.Join(t.TempDir(), "does-not-exist"))
	err := s.ensureClone(context.Background())
	if err == nil {
		t.Fatal("cloning from a non-existent remote reported success")
	}
	if !strings.Contains(err.Error(), "git") {
		t.Errorf("error does not say what failed: %v", err)
	}
	if err := s.cloneUsable(context.Background()); err == nil {
		t.Error("cloneUsable is happy after a failed clone; /readyz would lie")
	}
}
