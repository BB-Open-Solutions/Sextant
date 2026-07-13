// Package app holds the use-case services. Services depend only on ports
// and the domain; transport calls services, never adapters.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// FleetFile is the config-as-data document inside the overlay repo.
const FleetFile = "fleet.json"

// maxPushRetries bounds the rebase-retry loop on the HA write path. Config
// writes are human-paced; a handful of retries absorbs genuine races.
const maxPushRetries = 5

// ConfigService owns the configuration plane of one organisation: reads
// serve an immutable snapshot; writes run the safe transaction
// (mutate -> gate -> commit -> push) strictly serialized.
type ConfigService struct {
	repo ports.ConfigRepo
	gate ports.Gate

	// writeMu serializes the whole write transaction: one writer per repo.
	// The PoC ran this unlocked - the race was real, this is the fix.
	writeMu sync.Mutex

	// snap is the copy-on-write read snapshot: the fleet document and its
	// settings vocabulary (catalog.json, ADR 0005) from the same working
	// tree state, swapped as ONE pointer so readers can never observe a
	// fleet from one revision joined with a catalog from another. Readers
	// must treat both as immutable.
	snap atomic.Pointer[configSnapshot]
}

// configSnapshot pairs the fleet document with the catalog and hardware
// profiles it shipped with (same working-tree revision).
type configSnapshot struct {
	fleet    *fleet.Fleet
	catalog  *fleet.Catalog
	hardware *fleet.HardwareProfiles
}

// NewConfigService loads the initial snapshot and returns the service.
func NewConfigService(repo ports.ConfigRepo, gate ports.Gate) (*ConfigService, error) {
	s := &ConfigService{repo: repo, gate: gate}
	if _, err := s.reload(); err != nil {
		return nil, fmt.Errorf("load %s: %w", FleetFile, err)
	}
	return s, nil
}

// Fleet returns the current read snapshot. The returned document is shared
// and immutable; mutate only through Apply.
func (s *ConfigService) Fleet() *fleet.Fleet { return s.snap.Load().fleet }

// Catalog returns the settings vocabulary snapshot (never nil).
func (s *ConfigService) Catalog() *fleet.Catalog { return s.snap.Load().catalog }

// HardwareProfiles returns the hardware-profile catalog snapshot (never nil).
func (s *ConfigService) HardwareProfiles() *fleet.HardwareProfiles {
	return s.snap.Load().hardware
}

// Snapshot returns fleet and catalog from the same revision. Handlers that
// join the two must use this, not separate Fleet()/Catalog() calls, or a
// concurrent reload could hand them mismatched halves.
func (s *ConfigService) Snapshot() (*fleet.Fleet, *fleet.Catalog) {
	sn := s.snap.Load()
	return sn.fleet, sn.catalog
}

// reload re-reads fleet.json and catalog.json from the working tree into
// the snapshot.
func (s *ConfigService) reload() (*fleet.Fleet, error) {
	raw, err := s.repo.ReadFile(FleetFile)
	if err != nil {
		return nil, err
	}
	f, err := fleet.Decode(raw)
	if err != nil {
		return nil, err
	}
	// A missing catalog is a valid state (overlay predates the export); a
	// malformed one is not - the UI would silently lose its vocabulary.
	craw, err := s.repo.ReadFile(fleet.CatalogFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	cat, err := fleet.ParseCatalog(craw)
	if err != nil {
		return nil, err
	}
	// hardware-profiles.json is optional the same way: a missing file is a
	// valid overlay that predates the imaging surface; a malformed one is not.
	hraw, err := s.repo.ReadFile(fleet.HardwareProfilesFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	hw, err := fleet.ParseHardwareProfiles(hraw)
	if err != nil {
		return nil, err
	}
	s.snap.Store(&configSnapshot{fleet: f, catalog: cat, hardware: hw})
	return f, nil
}

// Apply runs the safe write transaction: load -> mutate -> gate -> commit
// (-> push, with rebase-retry on a lost race). On gate rejection the working
// file is restored and a *ports.ValidationError returned; nothing invalid
// ever reaches git. affectedHosts scopes the gate to the change's blast
// radius; empty validates the whole set.
func (s *ConfigService) Apply(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author, affectedHosts ...string) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if !s.repo.HasRemote() {
		return s.applyOnce(ctx, mut, msg, a, affectedHosts)
	}

	// HA path: sync to the remote, apply on the fresh base, push. On a lost
	// race the mutation re-runs on the new base - clean linear history.
	var lastErr error
	for i := 0; i < maxPushRetries; i++ {
		if err := s.repo.Sync(ctx); err != nil {
			return err // remote unreachable is not a conflict: fail fast
		}
		if _, err := s.reload(); err != nil {
			return err
		}
		if err := s.applyOnce(ctx, mut, msg, a, affectedHosts); err != nil {
			return err // mutation/gate errors are not retryable
		}
		err := s.repo.Push(ctx)
		if err == nil {
			return nil
		}
		if !errors.Is(err, ports.ErrConflict) {
			return err
		}
		lastErr = err
	}
	return fmt.Errorf("gave up after %d push conflicts: %w", maxPushRetries, lastErr)
}

// applyOnce runs the shared transaction against the service repo and
// refreshes the snapshot on success.
func (s *ConfigService) applyOnce(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author, hosts []string) error {
	f, err := applyTx(ctx, s.repo, s.gate, mut, msg, a, hosts)
	if err != nil {
		return err
	}
	// A write only touches fleet.json; the catalog and hardware profiles ride
	// along unchanged (they are separate overlay files).
	prev := s.snap.Load()
	s.snap.Store(&configSnapshot{fleet: f, catalog: prev.catalog, hardware: prev.hardware})
	return nil
}

// AuditLog returns the newest limit config commits when the repo adapter
// keeps history (git does; a plain-directory test repo may not).
func (s *ConfigService) AuditLog(ctx context.Context, limit int) ([]ports.AuditEntry, error) {
	al, ok := s.repo.(ports.AuditLog)
	if !ok {
		return nil, fmt.Errorf("audit log: %w", ports.ErrUnavailable)
	}
	return al.Log(ctx, limit)
}

// Reload re-reads the working tree into the snapshot (e.g. after a change
// request merged behind the service's back).
func (s *ConfigService) Reload() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_, err := s.reload()
	return err
}

// WithWriteLock runs fn while holding the single-writer lock. It lets another
// service in this package that mutates the same main-branch working tree (the
// change service, merging a change) serialize against the config write
// transaction instead of racing it on the shared index. fn may call the
// unexported reload() directly, since the lock is already held.
func (s *ConfigService) WithWriteLock(fn func() error) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return fn()
}

// SyncLoop keeps the working tree and snapshot in sync with the remote:
// the git remote is the source of truth, and commits made outside this
// console (engineers, CI, another replica) must become visible without a
// restart. Each tick takes the write lock, so a sync never interleaves
// with a write transaction; sync errors are logged and retried - a flaky
// remote must not kill the console. No-op without a remote.
func (s *ConfigService) SyncLoop(ctx context.Context, every time.Duration, log *slog.Logger) {
	if !s.repo.HasRemote() {
		return
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.writeMu.Lock()
			err := s.repo.Sync(ctx)
			if err == nil {
				_, err = s.reload()
			}
			s.writeMu.Unlock()
			if err != nil && ctx.Err() == nil {
				log.Warn("remote sync failed; keeping last good snapshot", "err", err)
			}
		}
	}
}

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

	restore := func() {
		if existed {
			_ = s.repo.WriteFile(path, orig)
		} else {
			_ = s.repo.RemoveFile(path)
		}
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

	if err := s.gate.Validate(ctx, s.repo.Dir(), nil); err != nil {
		restore()
		return err
	}
	return s.repo.Commit(ctx, msg, a, path)
}

// applyTx is the core write transaction, shared by direct writes and
// change-request edits (which run it against a worktree repo):
// load -> mutate -> write -> gate -> commit. Gate failure rolls the working
// file back to its original bytes; nothing invalid ever gets committed.
func applyTx(ctx context.Context, repo ports.ConfigRepo, gate ports.Gate, mut fleet.Mutation, msg string, a ports.Author, hosts []string) (*fleet.Fleet, error) {
	orig, err := repo.ReadFile(FleetFile)
	if err != nil {
		return nil, err
	}
	f, err := fleet.Decode(orig)
	if err != nil {
		return nil, err
	}
	if err := mut(f); err != nil {
		return nil, err
	}
	next, err := f.Encode()
	if err != nil {
		return nil, err
	}
	// Idempotent no-op: the mutation produced the exact same document.
	// Nothing to gate or commit; report success (the desired state holds).
	if bytes.Equal(next, orig) {
		return f, nil
	}
	if err := repo.WriteFile(FleetFile, next); err != nil {
		return nil, err
	}
	if err := gate.Validate(ctx, repo.Dir(), hosts); err != nil {
		if werr := repo.WriteFile(FleetFile, orig); werr != nil {
			// The working tree now holds the rejected edit, which would
			// become the base of the next write. Surface it loudly.
			return nil, errors.Join(err, fmt.Errorf("ROLLBACK FAILED, working tree dirty: %w", werr))
		}
		return nil, err
	}
	if err := repo.Commit(ctx, msg, a, FleetFile); err != nil {
		return nil, err
	}
	return f, nil
}
