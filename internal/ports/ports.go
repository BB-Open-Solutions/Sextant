// Package ports defines the interfaces the application layer depends on.
// Adapters (git, nix, postgres, oidc, ldap) implement them; the app never
// imports an adapter, so every effect is injectable and testable.
package ports

import (
	"context"
	"errors"
	"time"
)

// Author identifies who made a change (from the SSO session or API client),
// so every git commit carries real attribution. Subject is the stable
// principal id (OIDC subject / service name) used for segregation-of-duties
// checks; Name and Email feed git.
type Author struct {
	Subject string
	Name    string
	Email   string
}

// ErrConflict marks a write that lost a race against another writer (e.g. a
// non-fast-forward push). The transaction may re-run its mutation on the
// fresh base and retry.
var ErrConflict = errors.New("write conflict: remote advanced")

// ErrUnavailable marks a dependency that is not configured or reachable
// (e.g. the observed plane before Postgres is wired). Maps to 503.
var ErrUnavailable = errors.New("dependency unavailable")

// ValidationError means proposed configuration was rejected by the gate (the
// generator's injection-safe asserts / the NixOS module system). It is a
// user-data error, not a server fault; the edit has been rolled back.
type ValidationError struct{ Detail string }

func (e *ValidationError) Error() string { return "validation rejected: " + e.Detail }

// ConfigRepo is one organisation's overlay working tree: the git-backed home
// of fleet.json. Implementations must keep every operation repo-relative and
// never interpret file contents.
type ConfigRepo interface {
	// Dir returns the working tree path (the gate evaluates the flake there).
	Dir() string
	// ReadFile reads a repo-relative file. A file that does not exist MUST
	// return an error for which errors.Is(err, fs.ErrNotExist) is true -
	// callers (e.g. config.go) rely on that to treat an optional file (like
	// catalog.json) as simply absent rather than a load failure.
	ReadFile(name string) ([]byte, error)
	// WriteFile writes a repo-relative file (uncommitted).
	WriteFile(name string, data []byte) error
	// ListFiles lists the regular file names directly under a repo-relative
	// directory (empty when the directory does not exist).
	ListFiles(dir string) ([]string, error)
	// RemoveFile deletes a repo-relative file (uncommitted).
	RemoveFile(name string) error
	// Commit stages the given repo-relative files and commits them with the
	// author's attribution.
	Commit(ctx context.Context, msg string, a Author, files ...string) error
	// HasRemote reports whether this repo pushes to a remote (the HA path).
	HasRemote() bool
	// Sync fetches the remote and hard-resets the branch onto it, so the next
	// write applies against the latest committed config. No-op without remote.
	Sync(ctx context.Context) error
	// Push pushes HEAD to the remote branch. A lost race (remote advanced)
	// is reported as an error wrapping ErrConflict. No-op without remote.
	Push(ctx context.Context) error
}

// BranchRepo extends a config repo with the branch/worktree operations the
// change-request flow needs. The git adapter implements both.
type BranchRepo interface {
	// CreateBranch creates a branch at the current HEAD.
	CreateBranch(ctx context.Context, name string) error
	// DeleteBranch force-deletes a branch.
	DeleteBranch(ctx context.Context, name string) error
	// AddWorktree checks a branch out into dir as a linked worktree, so
	// change-request edits commit in isolation from the main tree.
	AddWorktree(ctx context.Context, dir, branch string) error
	// RemoveWorktree tears a linked worktree down.
	RemoveWorktree(ctx context.Context, dir string) error
	// MergeNoFF merges a branch into the current branch with a merge commit
	// (audit trail). On conflict it aborts, leaves the tree clean and
	// returns an error wrapping ErrConflict.
	MergeNoFF(ctx context.Context, branch, msg string, a Author) error
	// Diff returns the unified diff a branch would apply to the current
	// branch (merge-base three-dot semantics): what an approver reviews.
	Diff(ctx context.Context, branch string) (string, error)
}

// AuditEntry is one committed configuration change.
type AuditEntry struct {
	Hash    string    `json:"hash"`
	Author  string    `json:"author"`
	Email   string    `json:"email"`
	When    time.Time `json:"when"`
	Subject string    `json:"subject"`
}

// AuditLog extends a config repo with commit-history reads: the audit
// trail auditors and the console inspect. The git adapter implements it.
type AuditLog interface {
	// Log returns the newest limit commits on the current branch.
	Log(ctx context.Context, limit int) ([]AuditEntry, error)
}

// Builder runs the heavy build gate for a change: build the affected hosts'
// systems from the repo at dir. Implementations shell nix build (or a remote
// builder); tests inject fakes.
type Builder interface {
	Build(ctx context.Context, repoDir string, hosts []string) error
}

// Clock supplies time so services stay deterministic under test.
type Clock interface {
	Now() time.Time
}

// Gate validates a proposed configuration before it may be committed.
// hosts scopes validation to the devices a change can affect (blast
// radius); empty validates the whole set.
type Gate interface {
	Validate(ctx context.Context, repoDir string, hosts []string) error
}

// GateFunc adapts a function to the Gate interface (tests, gate mode none).
type GateFunc func(ctx context.Context, repoDir string, hosts []string) error

// Validate implements Gate.
func (f GateFunc) Validate(ctx context.Context, repoDir string, hosts []string) error {
	return f(ctx, repoDir, hosts)
}

// DirectoryGroup is one group in the identity provider's directory.
type DirectoryGroup struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Directory browses the IdP's group directory, feeding access-binding
// pickers so operators bind real groups instead of typing free text.
// Read-only by design: Sextant never manages the directory.
type Directory interface {
	// ListGroups returns groups matching query (substring; empty = all),
	// bounded by the adapter.
	ListGroups(ctx context.Context, query string) ([]DirectoryGroup, error)
}

// RefUpdater moves machine-owned git refs (the rings/<group> branches the
// update funnel steers, ADR 0011). Distinct from ConfigRepo writes: no
// working tree, no gate - these refs only ever point at commits that
// already passed the gate on main.
type RefUpdater interface {
	// SetRef points refs/heads/<name> at rev; reports whether it changed.
	SetRef(ctx context.Context, name, rev string) (bool, error)
	// PushRef force-pushes the ref to the remote (machine-owned; the
	// engine is the only writer). No-op without a remote.
	PushRef(ctx context.Context, name string) error
	// Head returns the current HEAD revision.
	Head(ctx context.Context) (string, error)
}
