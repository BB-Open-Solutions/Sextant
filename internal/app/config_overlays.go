package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// --- custom overlays (ADR 0014): console-authored Nix modules the generator
// imports, written through the same gated transaction as fleet.json. ---

const overlayDir = "overlays"

var overlayNameRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

func overlayPath(name string) string { return overlayDir + "/" + name + ".nix" }

// ListOverlays returns the names of the overlay modules in the repo.
func (s *ConfigService) ListOverlays() ([]string, error) {
	files, err := s.repo.ListFiles(overlayDir)
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, len(files))
	for _, f := range files {
		if n, ok := strings.CutSuffix(f, ".nix"); ok {
			out = append(out, n)
		}
	}
	return out, nil
}

// ReadOverlay returns one overlay module's source.
func (s *ConfigService) ReadOverlay(name string) (string, error) {
	if !overlayNameRE.MatchString(name) {
		return "", fmt.Errorf("overlay name %q must be a lowercase slug", name)
	}
	b, err := s.repo.ReadFile(overlayPath(name))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// WriteOverlay creates or replaces an overlay module. The write passes the Nix
// eval gate (the module must evaluate and the fleet must still build) before
// it commits; a module that does not evaluate never reaches git.
func (s *ConfigService) WriteOverlay(ctx context.Context, name, code string, a ports.Author) error {
	if !overlayNameRE.MatchString(name) {
		return fmt.Errorf("overlay name %q must be a lowercase slug", name)
	}
	if strings.TrimSpace(code) == "" {
		return fmt.Errorf("overlay %q has no content", name)
	}
	return s.auxApply(ctx, overlayPath(name), []byte(code), "overlays: write "+name, a)
}

// DeleteOverlay removes an overlay module. The gate then confirms no scope
// still references it (a dangling selection would fail the build).
func (s *ConfigService) DeleteOverlay(ctx context.Context, name string, a ports.Author) error {
	if !overlayNameRE.MatchString(name) {
		return fmt.Errorf("overlay name %q must be a lowercase slug", name)
	}
	return s.auxApply(ctx, overlayPath(name), nil, "overlays: remove "+name, a)
}

// auxApply runs the safe-write transaction for a repo file OTHER than
// fleet.json (content nil means delete), with the same serialization and
// HA push-retry as Apply. Overlays can be selected by any scope, so the gate
// validates the whole fleet (nil hosts).
func (s *ConfigService) auxApply(ctx context.Context, path string, content []byte, msg string, a ports.Author) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	if !s.repo.HasRemote() {
		return s.auxOnce(ctx, path, content, msg, a)
	}
	var lastErr error
	for i := 0; i < maxPushRetries; i++ {
		if err := s.repo.Sync(ctx); err != nil {
			return err
		}
		if err := s.auxOnce(ctx, path, content, msg, a); err != nil {
			return err
		}
		if err := s.repo.Push(ctx); err == nil {
			return nil
		} else if !errors.Is(err, ports.ErrConflict) {
			return err
		} else {
			lastErr = err
		}
	}
	return fmt.Errorf("gave up after %d push conflicts: %w", maxPushRetries, lastErr)
}

func (s *ConfigService) auxOnce(ctx context.Context, path string, content []byte, msg string, a ports.Author) error {
	orig, readErr := s.repo.ReadFile(path)
	existed := readErr == nil

	// restore undoes the working-tree edit after a gate rejection. Its OWN
	// failure must be loud (mirroring applyTx): a silently failed rollback
	// leaves rejected content as the base of the next write.
	restore := func() error {
		if existed {
			return s.repo.WriteFile(path, orig)
		}
		return s.repo.RemoveFile(path)
	}

	if content == nil { // delete
		if !existed {
			return nil // already gone: desired state holds
		}
		if err := s.repo.RemoveFile(path); err != nil {
			return err
		}
	} else { // create or replace
		if existed && bytes.Equal(orig, content) {
			return nil // idempotent no-op
		}
		if err := s.repo.WriteFile(path, content); err != nil {
			return err
		}
	}

	// An aux file (overlay, catalog) has no single-host blast radius, but the
	// shape classes DO capture which devices use which overlays/apps: one
	// representative per class proves the change against every distinct
	// configuration shape, same sampling rule as applyTx.
	if err := s.gate.Validate(ctx, s.repo.Dir(), s.Fleet().Representatives()); err != nil {
		if rerr := restore(); rerr != nil {
			return errors.Join(err, fmt.Errorf("ROLLBACK FAILED, working tree dirty: %w", rerr))
		}
		return err
	}
	return s.repo.Commit(ctx, msg, a, path)
}
