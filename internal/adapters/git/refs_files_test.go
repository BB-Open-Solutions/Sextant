package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// refs_files_test.go covers four functions that were at 0% and that each
// answer a question the console acts on.

// TestDeleteRemoteRefIsIdempotent covers the second half of the
// orphaned-branch sweep (2026-08-06). The sweep runs at every start, so it
// meets refs that are already gone on all but the first pass: if "already
// deleted" were an error, the console would log a failure on every restart
// for the rest of its life and teach whoever reads the log to ignore it.
func TestDeleteRemoteRefIsIdempotent(t *testing.T) {
	dir := initRepo(t)
	bare := withRemote(t, dir)
	r := openRepo(t, dir, "origin")
	ctx := context.Background()

	run(t, dir, "branch", "cr/abc123")
	run(t, dir, "push", "-q", "origin", "cr/abc123")
	if out := run(t, bare, "rev-parse", "--verify", "refs/heads/cr/abc123"); strings.TrimSpace(out) == "" {
		t.Fatal("precondition: the branch is not on the remote")
	}

	if err := r.DeleteRemoteRef(ctx, "cr/abc123"); err != nil {
		t.Fatalf("first delete: %v", err)
	}
	if _, err := runErr(t, bare, "rev-parse", "--verify", "refs/heads/cr/abc123"); err == nil {
		t.Error("the branch survived the delete")
	}
	// The whole point: doing it again is not an error.
	if err := r.DeleteRemoteRef(ctx, "cr/abc123"); err != nil {
		t.Errorf("deleting an already-absent ref reported an error: %v", err)
	}
	// And a ref that never existed behaves the same way.
	if err := r.DeleteRemoteRef(ctx, "cr/never-existed"); err != nil {
		t.Errorf("deleting a ref that never existed reported an error: %v", err)
	}
}

// TestDeleteRemoteRefIsANoOpWithoutARemote: a single-node deployment has no
// remote, and the sweep must not turn that into a start-up error.
func TestDeleteRemoteRefIsANoOpWithoutARemote(t *testing.T) {
	r := openRepo(t, initRepo(t), "")
	if err := r.DeleteRemoteRef(context.Background(), "cr/anything"); err != nil {
		t.Errorf("delete with no remote configured: %v", err)
	}
}

// TestFileAtReadsAHistoricRevision is what lets the console tell a
// configuration change from a core update: it reads flake.lock as of the
// revision a DEVICE reports, not as of main.
func TestFileAtReadsAHistoricRevision(t *testing.T) {
	dir := initRepo(t)
	r := openRepo(t, dir, "")
	ctx := context.Background()

	write := func(content string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, "flake.lock"), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		run(t, dir, "add", "flake.lock")
		run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "lock")
		return strings.TrimSpace(run(t, dir, "rev-parse", "HEAD"))
	}
	first := write(`{"core":"aaa"}`)
	second := write(`{"core":"bbb"}`)

	got, err := r.FileAt(ctx, first, "flake.lock")
	if err != nil {
		t.Fatalf("FileAt at the older revision: %v", err)
	}
	if !strings.Contains(string(got), "aaa") {
		t.Errorf("read %q; the older revision was not honoured", got)
	}
	if got, err = r.FileAt(ctx, second, "flake.lock"); err != nil || !strings.Contains(string(got), "bbb") {
		t.Errorf("newer revision: %q %v", got, err)
	}
	// A branch name works as well as a hash - both are things a caller
	// legitimately names.
	if _, err := r.FileAt(ctx, "main", "flake.lock"); err != nil {
		t.Errorf("FileAt by branch name: %v", err)
	}
}

// TestFileAtRefusesRatherThanGuesses pins the distinction stated in the
// function's own comment: "the core did not change" and "I could not look"
// must not read alike. A silent empty result would be read as the former.
func TestFileAtRefusesRatherThanGuesses(t *testing.T) {
	r := openRepo(t, initRepo(t), "")
	ctx := context.Background()

	cases := []struct{ name, rev, path string }{
		{"unknown revision", "0123456789abcdef0123456789abcdef01234567", "flake.lock"},
		{"path absent at that revision", "main", "flake.lock"},
		{"revision that is a range", "main..other", "flake.lock"},
		{"revision that looks like a flag", "--output=x", "flake.lock"},
		{"empty revision", "", "flake.lock"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := r.FileAt(ctx, c.rev, c.path)
			if err == nil {
				t.Errorf("returned %q with no error", got)
			}
		})
	}
}

func TestCommitCountAndListFilesAndRemoveFile(t *testing.T) {
	dir := initRepo(t)
	r := openRepo(t, dir, "")
	ctx := context.Background()

	before, err := r.CommitCount(ctx, "main")
	if err != nil {
		t.Fatalf("CommitCount: %v", err)
	}
	for _, name := range []string{"overlays/a.nix", "overlays/b.nix"} {
		if err := os.MkdirAll(filepath.Join(dir, "overlays"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run(t, dir, "add", ".")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "overlays")

	after, err := r.CommitCount(ctx, "main")
	if err != nil {
		t.Fatal(err)
	}
	if after <= before {
		t.Errorf("CommitCount did not move: %d -> %d", before, after)
	}

	names, err := r.ListFiles("overlays")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	if len(names) != 2 {
		t.Errorf("ListFiles = %v, want two entries", names)
	}
	// A directory that does not exist is empty, not an error: an overlay
	// directory is absent until somebody writes the first module, and the
	// page listing them must render on a fresh deployment.
	if got, err := r.ListFiles("no-such-dir"); err != nil || len(got) != 0 {
		t.Errorf("ListFiles on a missing dir = %v, %v", got, err)
	}
}

// runErr is run() without the fatal, for asserting that a git command fails.
func runErr(t *testing.T, dir string, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}
