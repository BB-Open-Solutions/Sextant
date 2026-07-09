package git

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"

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
	out, err := exec.CommandContext(ctx, "git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), strings.TrimSpace(string(out)))
	}
	return strings.TrimSpace(string(out)), nil
}

// CreateBranch implements ports.BranchRepo.
func (r *Repo) CreateBranch(ctx context.Context, name string) error {
	_, err := gitRun(ctx, r.dir, "branch", name)
	return err
}

// DeleteBranch implements ports.BranchRepo.
func (r *Repo) DeleteBranch(ctx context.Context, name string) error {
	_, err := gitRun(ctx, r.dir, "branch", "-D", name)
	return err
}

// AddWorktree implements ports.BranchRepo.
func (r *Repo) AddWorktree(ctx context.Context, dir, branch string) error {
	_, err := gitRun(ctx, r.dir, "worktree", "add", dir, branch)
	return err
}

// RemoveWorktree implements ports.BranchRepo.
func (r *Repo) RemoveWorktree(ctx context.Context, dir string) error {
	_, err := gitRun(ctx, r.dir, "worktree", "remove", "--force", dir)
	return err
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
	_, err := gitRun(ctx, r.dir,
		"-c", "user.name="+name, "-c", "user.email="+email,
		"merge", "--no-ff", "-m", msg, branch)
	if err == nil {
		return nil
	}
	if _, aerr := gitRun(ctx, r.dir, "merge", "--abort"); aerr != nil {
		return fmt.Errorf("merge failed AND abort failed, tree may be dirty: %w", err)
	}
	return fmt.Errorf("merge of %s conflicts with the base branch: %w: %w", branch, ports.ErrConflict, err)
}
