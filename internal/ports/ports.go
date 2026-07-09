// Package ports defines the interfaces the application layer depends on.
// Adapters (git, nix, postgres, oidc, ldap) implement them; the app never
// imports an adapter, so every effect is injectable and testable.
package ports

import (
	"context"
	"errors"
)

// Author identifies who made a change (from the SSO session or API client),
// so every git commit carries real attribution.
type Author struct {
	Name  string
	Email string
}

// ErrConflict marks a write that lost a race against another writer (e.g. a
// non-fast-forward push). The transaction may re-run its mutation on the
// fresh base and retry.
var ErrConflict = errors.New("write conflict: remote advanced")

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
	// ReadFile reads a repo-relative file.
	ReadFile(name string) ([]byte, error)
	// WriteFile writes a repo-relative file (uncommitted).
	WriteFile(name string, data []byte) error
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
