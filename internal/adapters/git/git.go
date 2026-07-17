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

// netTimeout bounds a single remote git operation (fetch/push). Without it a
// hung remote (dropped TCP, dead forge) inherits git's own long timeouts and,
// because these run under the single-writer lock, would stall every config
// write behind them. A caller ctx with a shorter deadline still wins.
const netTimeout = 60 * time.Second

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
	// #nosec G304 - p is confined to the repo root by safePath (rejects absolute/.. and resolves symlinks before use).
	return os.ReadFile(p)
}

// WriteFile implements ports.ConfigRepo. Parent directories are created so a
// nested file (e.g. overlays/<name>.nix) can be written into a fresh tree.
func (r *Repo) WriteFile(name string, data []byte) error {
	p, err := r.safePath(name)
	if err != nil {
		return err
	}
	// #nosec G301 - the config repo holds public fleet/overlay source under safePath confinement, not secrets; 0755 is fine.
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	// #nosec G306 - fleet.json/overlays are non-secret config committed to git; world-readable is intended.
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

// safePath confines repo-relative names to the working tree. The lexical
// check alone stops "../" traversal in the name itself but not a symlink
// planted inside the tree - e.g. by a merged change branch - whose target
// points outside the repo; WriteFile/RemoveFile would then follow the link
// and touch the real filesystem there. So after the lexical check we also
// resolve symlinks on the nearest existing ancestor of the target and
// re-confirm it still resolves under the repo root. Walking up to the
// nearest existing ancestor (rather than requiring the immediate parent to
// exist) keeps WriteFile's "create a fresh nested file" case working, since
// its parent directories may not exist yet.
func (r *Repo) safePath(name string) (string, error) {
	if name == "" || filepath.IsAbs(name) {
		return "", fmt.Errorf("bad repo path %q", name)
	}
	p := filepath.Join(r.dir, name)
	sep := string(filepath.Separator)
	if rel, err := filepath.Rel(r.dir, p); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+sep) {
		return "", fmt.Errorf("path %q escapes the repo", name)
	}

	realRoot, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		return "", fmt.Errorf("resolve repo root: %w", err)
	}
	anchor, err := nearestExisting(filepath.Dir(p))
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", name, err)
	}
	realAnchor, err := filepath.EvalSymlinks(anchor)
	if err != nil {
		return "", fmt.Errorf("resolve %q: %w", name, err)
	}
	if rel, err := filepath.Rel(realRoot, realAnchor); err != nil || rel == ".." || strings.HasPrefix(rel, ".."+sep) {
		return "", fmt.Errorf("path %q escapes the repo", name)
	}
	return p, nil
}

// nearestExisting walks up from dir until it finds a path that exists,
// returning that path (dir itself if it already exists). The repo root is
// always a valid stopping point since Open requires it to exist.
func nearestExisting(dir string) (string, error) {
	for {
		if _, err := os.Lstat(dir); err == nil {
			return dir, nil
		} else if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return dir, nil
		}
		dir = parent
	}
}

// Commit implements ports.ConfigRepo. The author comes from the session so
// the audit trail names a person, with a service fallback.
func (r *Repo) Commit(ctx context.Context, msg string, a ports.Author, files ...string) error {
	addArgs := append([]string{"-C", r.dir, "add", "--"}, files...)
	// #nosec G204 - fixed "git" binary, argv slice (no shell); "--" terminates options so file names cannot become flags.
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
	// #nosec G204 - fixed "git" binary with an argv slice (no shell); name/email/msg are passed as discrete args, not interpolated into a command line.
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
	// #nosec G204 - fixed "git" binary and a constant argv (no shell, no dynamic input).
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
	if msg, err := r.runNet(ctx, "fetch", r.remote); err != nil {
		return fmt.Errorf("git fetch: %s", msg)
	}
	rctx, cancel := context.WithTimeout(ctx, netTimeout)
	defer cancel()
	ref := r.remote + "/" + r.branch(rctx)
	if out, err := exec.CommandContext(rctx, "git", "-C", r.dir, "reset", "--hard", ref).CombinedOutput(); err != nil {
		return fmt.Errorf("git reset --hard %s: %s", ref, strings.TrimSpace(string(out)))
	}
	return nil
}

// runNet runs one git network command, retrying twice on transient network
// hiccups (a DNS blip mid-save must not read as a hard failure to the
// operator). Non-transient errors return immediately with their message.
func (r *Repo) runNet(ctx context.Context, args ...string) (string, error) {
	var msg string
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return msg, err
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		actx, cancel := context.WithTimeout(ctx, netTimeout)
		out, e := exec.CommandContext(actx, "git", append([]string{"-C", r.dir}, args...)...).CombinedOutput()
		cancel()
		if e == nil {
			return "", nil
		}
		msg, err = strings.TrimSpace(string(out)), e
		if !isTransientNet(msg) {
			return msg, err
		}
	}
	return msg, err
}

// isTransientNet classifies one-off network failures worth a quick retry.
func isTransientNet(msg string) bool {
	m := strings.ToLower(msg)
	for _, s := range []string{
		"could not resolve host",
		"temporary failure in name resolution",
		"connection refused",
		"connection reset",
		"connection timed out",
		"operation timed out",
		"early eof",
	} {
		if strings.Contains(m, s) {
			return true
		}
	}
	return false
}

// Push implements ports.ConfigRepo. A push rejected because the remote
// advanced (a concurrent writer won) wraps ports.ErrConflict so the caller
// can re-apply and retry; other failures are terminal.
func (r *Repo) Push(ctx context.Context) error {
	if r.remote == "" {
		return nil
	}
	bctx, cancel := context.WithTimeout(ctx, netTimeout)
	branch := r.branch(bctx)
	cancel()
	msg, err := r.runNet(ctx, "push", r.remote, "HEAD:"+branch)
	if err == nil {
		return nil
	}
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
	// "--end-of-options" stops a caller-influenced rev (the rollout target) from
	// being misread as a git option, mirroring the "--" guards elsewhere here.
	full, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"rev-parse", "--verify", "--end-of-options", rev+"^{commit}").Output()
	if err != nil {
		return false, fmt.Errorf("git rev-parse %s: unknown revision", rev)
	}
	target := strings.TrimSpace(string(full))
	if strings.TrimSpace(string(cur)) == target {
		return false, nil
	}
	// "--" stops a ref built from a caller-influenced name from being
	// misread as a flag (mirrors Commit's "git add --").
	if out, err := exec.CommandContext(ctx, "git", "-C", r.dir,
		"update-ref", "--", ref, target).CombinedOutput(); err != nil {
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
	// "--" stops the repository/refspec pair from being misread as flags
	// (mirrors Commit's "git add --").
	if msg, err := r.runNet(ctx, "push", "--force", "--", r.remote, ref+":"+ref); err != nil {
		return fmt.Errorf("git push ref %s: %s", name, msg)
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
