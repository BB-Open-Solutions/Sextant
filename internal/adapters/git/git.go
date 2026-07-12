// Package git implements ports.ConfigRepo against the system git binary.
// Every operation is an exec with an argument vector (no shell), scoped to
// the repo directory with -C. The adapter never interprets file contents.
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Repo is one git working tree acting as a config repo.
type Repo struct {
	dir string
	// remote is the push remote name (e.g. "origin"); empty disables the
	// HA sync/push path (Tier 0: local commits only).
	remote string
}

// Open returns a Repo for an existing working tree. The directory must
// contain a git repository (a .git directory, or a .git file for a linked
// worktree); callers provision repos out of band.
func Open(dir, remote string) (*Repo, error) {
	if _, err := os.Stat(filepath.Join(dir, ".git")); err != nil {
		return nil, fmt.Errorf("%s is not a git working tree", dir)
	}
	return &Repo{dir: dir, remote: remote}, nil
}

// Dir implements ports.ConfigRepo.
func (r *Repo) Dir() string { return r.dir }

// HasRemote implements ports.ConfigRepo.
func (r *Repo) HasRemote() bool { return r.remote != "" }

// ReadFile implements ports.ConfigRepo.
func (r *Repo) ReadFile(name string) ([]byte, error) {
	p, err := r.safePath(name)
	if err != nil {
		return nil, err
	}
	return os.ReadFile(p)
}

// WriteFile implements ports.ConfigRepo. Parent directories are created so a
// nested file (e.g. overlays/<name>.nix) can be written into a fresh tree.
func (r *Repo) WriteFile(name string, data []byte) error {
	p, err := r.safePath(name)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, data, 0o644)
}

// ListFiles implements ports.ConfigRepo: the regular file names directly under
// a repo-relative directory, sorted. A missing directory is empty, not an error.
func (r *Repo) ListFiles(dir string) ([]string, error) {
	p, err := r.safePath(dir)
	if err != nil {
		return nil, err
	}
	ents, err := os.ReadDir(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []string
	for _, e := range ents {
		if !e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// RemoveFile implements ports.ConfigRepo.
func (r *Repo) RemoveFile(name string) error {
	p, err := r.safePath(name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// safePath confines repo-relative names to the working tree.
func (r *Repo) safePath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("bad repo path %q", name)
	}
	p := filepath.Join(r.dir, name)
	if rel, err := filepath.Rel(r.dir, p); err != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("path %q escapes the repo", name)
	}
	return p, nil
}

// Commit implements ports.ConfigRepo. The author comes from the session so
// the audit trail names a person, with a service fallback.
func (r *Repo) Commit(ctx context.Context, msg string, a ports.Author, files ...string) error {
	addArgs := append([]string{"-C", r.dir, "add", "--"}, files...)
	if out, err := exec.CommandContext(ctx, "git", addArgs...).CombinedOutput(); err != nil {
		return fmt.Errorf("git add: %s", strings.TrimSpace(string(out)))
	}
	name := a.Name
	if name == "" {
		name = "sextant"
	}
	email := a.Email
	if email == "" {
		email = "sextant@localhost"
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"-c", "user.name="+name, "-c", "user.email="+email,
		"commit", "-m", msg).CombinedOutput()
	if err != nil {
		return fmt.Errorf("git commit: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// branch returns the current branch name ("main" when detached or unknown).
func (r *Repo) branch(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "rev-parse", "--abbrev-ref", "HEAD").Output()
	b := strings.TrimSpace(string(out))
	if err != nil || b == "" || b == "HEAD" {
		return "main"
	}
	return b
}

// Sync implements ports.ConfigRepo: fetch and hard-reset onto the remote
// branch so the next write applies on the latest committed config.
// Untracked files are not touched by the reset.
func (r *Repo) Sync(ctx context.Context) error {
	if r.remote == "" {
		return nil
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "fetch", r.remote).CombinedOutput(); err != nil {
		return fmt.Errorf("git fetch: %s", strings.TrimSpace(string(out)))
	}
	ref := r.remote + "/" + r.branch(ctx)
	if out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "reset", "--hard", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard %s: %s", ref, strings.TrimSpace(string(out)))
	}
	return nil
}

// Push implements ports.ConfigRepo. A push rejected because the remote
// advanced (a concurrent writer won) wraps ports.ErrConflict so the caller
// can re-apply and retry; other failures are terminal.
func (r *Repo) Push(ctx context.Context) error {
	if r.remote == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"push", r.remote, "HEAD:"+r.branch(ctx)).CombinedOutput()
	if err == nil {
		return nil
	}
	msg := strings.TrimSpace(string(out))
	if isNonFastForward(msg) {
		return fmt.Errorf("git push: %s: %w", msg, ports.ErrConflict)
	}
	return fmt.Errorf("git push: %s", msg)
}

// isNonFastForward classifies a lost push race across git version wordings.
func isNonFastForward(msg string) bool {
	m := strings.ToLower(msg)
	return strings.Contains(m, "non-fast-forward") ||
		strings.Contains(m, "fetch first") ||
		strings.Contains(m, "updates were rejected") ||
		strings.Contains(m, "! [rejected]")
}

// Log implements ports.AuditLog: the newest limit commits, machine-parsed
// with unit separators so subjects may contain anything but 0x1f/newline.
func (r *Repo) Log(ctx context.Context, limit int) ([]ports.AuditEntry, error) {
	if limit <= 0 || limit > 1000 {
		limit = 100
	}
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "log",
		fmt.Sprintf("-n%d", limit), "--format=%H%x1f%an%x1f%ae%x1f%at%x1f%s").Output()
	if err != nil {
		return nil, fmt.Errorf("git log: %w", err)
	}
	var entries []ports.AuditEntry
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.Split(line, "\x1f")
		if len(parts) != 5 {
			continue
		}
		sec, err := strconv.ParseInt(parts[3], 10, 64)
		if err != nil {
			continue
		}
		entries = append(entries, ports.AuditEntry{
			Hash: parts[0], Author: parts[1], Email: parts[2],
			When: time.Unix(sec, 0).UTC(), Subject: parts[4],
		})
	}
	return entries, nil
}

// SetRef implements ports.RefUpdater.
func (r *Repo) SetRef(ctx context.Context, name, rev string) (bool, error) {
	ref := "refs/heads/" + name
	cur, _ := exec.CommandContext(ctx, "git", "-C", r.dir, "rev-parse", "--verify", "-q", ref).Output()
	// Resolve rev to a full hash so short revisions compare correctly.
	full, err := exec.CommandContext(ctx, "git", "-C", r.dir, "rev-parse", "--verify", rev+"^{commit}").Output()
	if err != nil {
		return false, fmt.Errorf("git rev-parse %s: unknown revision", rev)
	}
	target := strings.TrimSpace(string(full))
	if strings.TrimSpace(string(cur)) == target {
		return false, nil
	}
	if out, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"update-ref", ref, target).CombinedOutput(); err != nil {
		return false, fmt.Errorf("git update-ref: %s", strings.TrimSpace(string(out)))
	}
	return true, nil
}

// PushRef implements ports.RefUpdater. Ring refs are machine-owned: the
// rollout engine is the only writer, so a force push is safe and needed
// (a re-targeted rollout can move a ref backwards).
func (r *Repo) PushRef(ctx context.Context, name string) error {
	if r.remote == "" {
		return nil
	}
	ref := "refs/heads/" + name
	if out, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"push", "--force", r.remote, ref+":"+ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git push ref %s: %s", name, strings.TrimSpace(string(out)))
	}
	return nil
}

// Head implements ports.RefUpdater.
func (r *Repo) Head(ctx context.Context) (string, error) {
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "rev-parse", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git rev-parse HEAD: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}
