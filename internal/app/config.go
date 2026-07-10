// Package app holds the use-case services. Services depend only on ports
// and the domain; transport calls services, never adapters.
package app

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"

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

	// snap is the copy-on-write read snapshot. Readers get a consistent
	// *fleet.Fleet without touching disk; every successful write or sync
	// replaces it atomically. Readers must treat it as immutable.
	snap atomic.Pointer[fleet.Fleet]
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
func (s *ConfigService) Fleet() *fleet.Fleet { return s.snap.Load() }

// reload re-reads fleet.json from the working tree into the snapshot.
func (s *ConfigService) reload() (*fleet.Fleet, error) {
	raw, err := s.repo.ReadFile(FleetFile)
	if err != nil {
		return nil, err
	}
	f, err := fleet.Decode(raw)
	if err != nil {
		return nil, err
	}
	s.snap.Store(f)
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
	s.snap.Store(f)
	return nil
}

// Reload re-reads the working tree into the snapshot (e.g. after a change
// request merged behind the service's back).
func (s *ConfigService) Reload() error {
	_, err := s.reload()
	return err
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
