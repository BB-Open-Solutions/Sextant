// Package git implements ports.ConfigRepo against the system git binary.
// Every operation is an exec with an argument vector (no shell), scoped to
// the repo directory with -C. The adapter never interprets file contents.
package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// CommitCount returns the number of commits reachable from rev - a
// monotonic "release number" for lineage-ordered revisions on one branch.
// Humans compare 142 vs 145 at a glance where two sha prefixes say nothing.
func (r *Repo) CommitCount(ctx context.Context, rev string) (int, error) {
	// #nosec G204 - fixed "git" binary; rev is passed as a discrete arg and
	// "--" cannot help here (rev-list takes revs before paths), but a
	// malformed rev simply fails to resolve.
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "rev-list", "--count", rev).Output()
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s: %w", rev, err)
	}
	n, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return 0, fmt.Errorf("git rev-list --count %s: %w", rev, err)
	}
	return n, nil
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

// SourceKey fingerprints the committed tree EXCLUDING one path: every blob the
// flake evaluates from, minus the file the caller is about to change.
//
// It exists so a gate verdict can be memoised. HEAD moves on every config
// commit, so keying a verdict on HEAD would invalidate the whole cache with
// each edit and prove nothing was gained. What actually decides a device's
// evaluation, apart from its own resolved settings, is the REST of the tree:
// flake.lock (which pins the core and nixpkgs), the generator, the hardware
// profiles, the catalog. Those change on their own commits, and when they do
// every verdict must fall.
//
// The hash is over the `<mode> <type> <sha> <path>` lines git already
// computes, so it is exact rather than heuristic and costs one process.
//
// HEAD alone would not be enough. The gate evaluates the WORKING TREE, and a
// caller memoising against this key is asserting that the tree differs from
// HEAD in the excluded path only. So the uncommitted state goes into the hash
// too: any other modified or untracked file moves the key and every verdict
// falls, which is the safe direction. Without that, an overlay file left dirty
// by another path would change what nix evaluates while the key stood still.
func (r *Repo) SourceKey(ctx context.Context, exclude string) (string, error) {
	tree, err := exec.CommandContext(ctx, "git", "-C", r.dir, "ls-tree", "-r", "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git ls-tree HEAD: %w", err)
	}
	// --porcelain is the stable machine format; its `XY path` lines cover
	// modified, staged, deleted and (with -uall) untracked files.
	status, err := exec.CommandContext(ctx, "git", "-C", r.dir, "status", "--porcelain", "-uall").Output()
	if err != nil {
		return "", fmt.Errorf("git status --porcelain: %w", err)
	}
	h := sha256.New()
	for _, section := range []string{string(tree), string(status)} {
		for _, line := range strings.Split(section, "\n") {
			// A tree line ends in \t<path>; a status line ends in <space><path>.
			// Skipping the excluded path in both is what makes an edit to it
			// invisible here - that is the whole point of the exclusion.
			if line == "" || strings.HasSuffix(line, "\t"+exclude) || strings.HasSuffix(line, " "+exclude) {
				continue
			}
			h.Write([]byte(line))
			h.Write([]byte{'\n'})
		}
		h.Write([]byte{'\x00'})
	}
	return hex.EncodeToString(h.Sum(nil)[:16]), nil
}

// RemoteHead resolves the HEAD revision of an arbitrary remote without a
// local clone - the upstream watcher's one git need.
func RemoteHead(ctx context.Context, remote string) (string, error) {
	// #nosec G204 - fixed "git" binary, argv slice; the remote URL comes from
	// server configuration, not request input.
	out, err := exec.CommandContext(ctx, "git", "ls-remote", remote, "HEAD").Output()
	if err != nil {
		return "", fmt.Errorf("git ls-remote %s: %w", remote, err)
	}
	fields := strings.Fields(strings.TrimSpace(string(out)))
	if len(fields) < 1 {
		return "", nil
	}
	return fields[0], nil
}

// FileAt returns a repo-relative file's contents as of a revision. Used to
// read flake.lock at the revision a DEVICE reports running, so the console can
// tell a configuration change from a core update: two config revisions that
// pin the same core are the same VERSION of the system, however many commits
// apart they are.
//
// A revision this clone does not have, or a path absent at it, is an error -
// "the core did not change" and "I could not look" must not read alike.
func (r *Repo) FileAt(ctx context.Context, rev, path string) ([]byte, error) {
	if !revishRe.MatchString(rev) {
		return nil, fmt.Errorf("invalid revision %q", rev)
	}
	// "--" separates the rev:path spec from any pathspec; the spec itself is
	// built from a validated rev and a caller-controlled constant path.
	// #nosec G204 - fixed "git" binary, discrete args, no shell.
	out, err := exec.CommandContext(ctx, "git", "-C", r.dir, "show", rev+":"+path, "--").Output()
	if err != nil {
		return nil, fmt.Errorf("git show %s:%s: %w", rev, path, err)
	}
	return out, nil
}

// revishRe accepts a commit hash or a branch/tag name, the two things a caller
// legitimately names. Deliberately narrow: no "..", no leading "-", nothing
// that could be read as a flag or a range.
var revishRe = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]*$`)
