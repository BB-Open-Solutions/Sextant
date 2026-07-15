package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// gitTimeout bounds every branch/worktree/merge invocation: a hung git
// (lock contention, credential prompt) must never block the service
// indefinitely. Ported from the proven PoC bound.
const gitTimeout = 60 * time.Second

// run executes git -C dir with a hard timeout.
func gitRun(ctx context.Context, dir string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()
	// #nosec G204 - fixed "git" binary with a code-controlled argv slice (no shell); branch/ref args are validated before reaching here.
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateBranch implements ports.BranchRepo. The "--" stops a name starting
// with "-" from being misread as a flag (mirrors Commit's "git add --").
func (r *Repo) CreateBranch(ctx context.Context, name string) error {
	_, err := gitRun(ctx, r.dir, "branch", "--", name)
	return err
}

// DeleteBranch implements ports.BranchRepo. Same "--" guard as CreateBranch.
func (r *Repo) DeleteBranch(ctx context.Context, name string) error {
	_, err := gitRun(ctx, r.dir, "branch", "-D", "--", name)
	return err
}

// AddWorktree implements ports.BranchRepo. Same "--" guard as CreateBranch:
// it stops a dir/branch starting with "-" from being misread as a flag.
func (r *Repo) AddWorktree(ctx context.Context, dir, branch string) error {
	_, err := gitRun(ctx, r.dir, "worktree", "add", "--", dir, branch)
	return err
}

// RemoveWorktree implements ports.BranchRepo. Same "--" guard as CreateBranch.
func (r *Repo) RemoveWorktree(ctx context.Context, dir string) error {
	_, err := gitRun(ctx, r.dir, "worktree", "remove", "--force", "--", dir)
	return err
}

// maxDiffBytes bounds a diff so one pathological change cannot flood the
// API or the approver's browser.
const maxDiffBytes = 512 << 10

// Diff implements ports.BranchRepo: the changes the branch introduces
// relative to the merge base (three-dot), unified format. The trailing "--"
// guards against a future caller passing branch as a separate positional
// arg (today it is folded into the "HEAD...<branch>" range spec, which
// itself cannot start with "-", but the guard keeps the call site aligned
// with every other git invocation here regardless of how it evolves).
func (r *Repo) Diff(ctx context.Context, branch string) (string, error) {
	out, err := gitRun(ctx, r.dir, "diff", "HEAD..."+branch, "--")
	if err != nil {
		return "", err
	}
	return truncateDiff(out), nil
}

// truncateDiff bounds a diff to maxDiffBytes so one pathological change cannot
// flood the API or an approver's browser. The cut backs off byte-by-byte while
// the tail is an incomplete/invalid UTF-8 sequence, so the raw byte cutoff
// never splits a multibyte rune (which would corrupt the diff text).
func truncateDiff(out string) string {
	if len(out) <= maxDiffBytes {
		return out
	}
	cut := out[:maxDiffBytes]
	for len(cut) > 0 {
		r, size := utf8.DecodeLastRuneInString(cut)
		if r != utf8.RuneError || size > 1 {
			break
		}
		cut = cut[:len(cut)-1]
	}
	return cut + "\n... (diff truncated)"
}

// MergeNoFF implements ports.BranchRepo: merge with a merge commit for the
// audit trail. A conflict aborts the merge (leaving the tree clean) and
// reports ErrConflict so the caller can ask for a rebase.
func (r *Repo) MergeNoFF(ctx context.Context, branch, msg string, a ports.Author) error {
	name, email := a.Name, a.Email
	if name == "" {
		name = "sextant"
	}
	if email == "" {
		email = "sextant@localhost"
	}
	// "--" guards branch the same way CreateBranch/AddWorktree do: without
	// it, a branch name starting with "-" could be read as a git flag
	// instead of the merge target. Unreachable today (branch is always
	// cr/<slug>), but keeps every caller-influenced positional in this
	// package consistently guarded regardless of how callers evolve.
	_, err := gitRun(ctx, r.dir,
		"-c", "user.name="+name, "-c", "user.email="+email,
		"merge", "--no-ff", "-m", msg, "--", branch)
	if err == nil {
		return nil
	}
	if _, aerr := gitRun(ctx, r.dir, "merge", "--abort"); aerr != nil {
		return fmt.Errorf("merge failed AND abort failed, tree may be dirty: %w", err)
	}
	return fmt.Errorf("merge of %s conflicts with the base branch: %w: %w", branch, ports.ErrConflict, err)
}
