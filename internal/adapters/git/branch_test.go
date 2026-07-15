package git

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func openRepo(t *testing.T, dir, remote string) *Repo {
	t.Helper()
	r, err := Open(dir, remote)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestBranchAndWorktreeLifecycle(t *testing.T) {
	dir := initRepo(t)
	r := openRepo(t, dir, "")
	ctx := context.Background()

	if err := r.CreateBranch(ctx, "cr-1"); err != nil {
		t.Fatalf("CreateBranch: %v", err)
	}
	wt := filepath.Join(t.TempDir(), "wt")
	if err := r.AddWorktree(ctx, wt, "cr-1"); err != nil {
		t.Fatalf("AddWorktree: %v", err)
	}
	// The worktree has the branch checked out and is writable.
	if err := os.WriteFile(filepath.Join(wt, "f.txt"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, wt, "add", "f.txt")
	run(t, wt, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "add f")

	if err := r.RemoveWorktree(ctx, wt); err != nil {
		t.Fatalf("RemoveWorktree: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatal("worktree dir still present after remove")
	}
	if err := r.DeleteBranch(ctx, "cr-1"); err != nil {
		t.Fatalf("DeleteBranch: %v", err)
	}
	if out := run(t, dir, "branch", "--list", "cr-1"); out != "" {
		t.Fatalf("branch still listed: %q", out)
	}
}

func TestDiffShowsBranchChanges(t *testing.T) {
	dir := initRepo(t)
	r := openRepo(t, dir, "")
	ctx := context.Background()

	run(t, dir, "checkout", "-q", "-b", "feature")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte("{\"v\":3}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "add", "fleet.json")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "add fleet")
	run(t, dir, "checkout", "-q", "main")

	diff, err := r.Diff(ctx, "feature")
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !strings.Contains(diff, "fleet.json") || !strings.Contains(diff, "+{\"v\":3}") {
		t.Fatalf("diff missing the change:\n%s", diff)
	}
}

func TestTruncateDiff(t *testing.T) {
	// A diff within the cap passes through byte-for-byte.
	small := "diff --git a/x b/x\n+hello\n"
	if got := truncateDiff(small); got != small {
		t.Fatalf("small diff altered: %q", got)
	}

	// An oversized diff is cut to the marker and stays valid UTF-8.
	big := strings.Repeat("a", maxDiffBytes+1000)
	got := truncateDiff(big)
	if !strings.HasSuffix(got, "\n... (diff truncated)") {
		t.Fatal("truncated diff missing the marker")
	}
	if len(got) > maxDiffBytes+len("\n... (diff truncated)") {
		t.Fatalf("truncated diff still over the cap: %d bytes", len(got))
	}

	// The cut must never split a multibyte rune. maxDiffBytes is not a
	// multiple of 3, so filling with 3-byte runes forces a naive byte cut to
	// land mid-rune; the backoff must leave the body rune-clean.
	multibyte := strings.Repeat("世", maxDiffBytes/3+50) // 3 bytes each
	body := strings.TrimSuffix(truncateDiff(multibyte), "\n... (diff truncated)")
	if !utf8.ValidString(body) {
		t.Fatal("truncation split a multibyte rune (body not valid UTF-8)")
	}
	if strings.ContainsRune(body, utf8.RuneError) {
		t.Fatal("truncated body contains U+FFFD (split rune)")
	}
}

func TestMergeNoFFAndConflict(t *testing.T) {
	dir := initRepo(t)
	r := openRepo(t, dir, "")
	ctx := context.Background()

	// Clean merge: a branch that touches a new file.
	run(t, dir, "checkout", "-q", "-b", "clean")
	os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644)
	run(t, dir, "add", "a.txt")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "a")
	run(t, dir, "checkout", "-q", "main")
	if err := r.MergeNoFF(ctx, "clean", "merge clean", ports.Author{Name: "op", Email: "op@x"}); err != nil {
		t.Fatalf("clean MergeNoFF: %v", err)
	}

	// Conflict: main and branch change the same line differently.
	run(t, dir, "checkout", "-q", "-b", "conflict")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("branch\n"), 0o644)
	run(t, dir, "add", "c.txt")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "branch c")
	run(t, dir, "checkout", "-q", "main")
	os.WriteFile(filepath.Join(dir, "c.txt"), []byte("main\n"), 0o644)
	run(t, dir, "add", "c.txt")
	run(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-qm", "main c")

	err := r.MergeNoFF(ctx, "conflict", "merge conflict", ports.Author{})
	if !errors.Is(err, ports.ErrConflict) {
		t.Fatalf("want ErrConflict, got %v", err)
	}
	// The tree is clean after the aborted merge (no MERGE_HEAD).
	if _, statErr := os.Stat(filepath.Join(dir, ".git", "MERGE_HEAD")); !os.IsNotExist(statErr) {
		t.Fatal("merge not aborted; MERGE_HEAD present")
	}
}

func TestLogSetRefPushHead(t *testing.T) {
	dir := initRepo(t)
	remote := withRemote(t, dir)
	r := openRepo(t, dir, "origin")
	ctx := context.Background()

	// Head + Dir.
	head, err := r.Head(ctx)
	if err != nil || len(head) != 40 {
		t.Fatalf("Head = %q, %v", head, err)
	}
	if r.Dir() != dir {
		t.Fatalf("Dir = %q, want %q", r.Dir(), dir)
	}

	// Log returns the initial commit as an audit entry.
	entries, err := r.Log(ctx, 10)
	if err != nil || len(entries) == 0 {
		t.Fatalf("Log = %v, %v", entries, err)
	}
	if entries[0].Subject != "init" || entries[0].Email != "t@t" {
		t.Fatalf("audit entry wrong: %+v", entries[0])
	}

	// SetRef creates a ring ref at HEAD (changed=true), and is idempotent.
	changed, err := r.SetRef(ctx, "rings/pilot", head)
	if err != nil || !changed {
		t.Fatalf("SetRef create = %v, %v", changed, err)
	}
	changed, err = r.SetRef(ctx, "rings/pilot", head)
	if err != nil || changed {
		t.Fatalf("SetRef no-op = %v, %v", changed, err)
	}
	if _, err := r.SetRef(ctx, "rings/pilot", "deadbeef"); err == nil {
		t.Fatal("SetRef accepted an unknown revision")
	}

	// PushRef pushes the ring ref to the remote.
	if err := r.PushRef(ctx, "rings/pilot"); err != nil {
		t.Fatalf("PushRef: %v", err)
	}
	if out := run(t, remote, "rev-parse", "refs/heads/rings/pilot"); strings.TrimSpace(out) != head {
		t.Fatalf("remote ref = %q, want %q", out, head)
	}
}
