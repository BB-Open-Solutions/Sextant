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

// ErrChangeRequestRequired is returned when the organisation mandates that
// configuration edits flow through a reviewed change request, so a direct
// edit to main is refused. It is a sentinel so every transport (web, API)
// enforces the same governance instead of each re-implementing the check.
var ErrChangeRequestRequired = errors.New("change-request required: this organisation reviews configuration edits before they take effect - stage this change on the Changes page")

// ConfigService owns the configuration plane of one organisation: reads
// serve an immutable snapshot; writes run the safe transaction
// (mutate -> gate -> commit -> push) strictly serialized.
type ConfigService struct {
	repo ports.ConfigRepo
	gate ports.Gate

	// writeMu serializes the whole write transaction: one writer per repo.
	// The PoC ran this unlocked - the race was real, this is the fix.
	writeMu sync.Mutex

	// relCache memoises revision -> release number (immutable once known).
	relCache sync.Map
	// coreAtCache memoises CoreVersionAt: a revision pins one core forever.
	coreAtCache sync.Map

	// coreCache memoises the overlay's core pin against the snapshot it was
	// read with (see coreversion.go).
	coreCache atomic.Pointer[coreEntry]

	// verdicts memoises gate acceptances per (source, globals, shape), so an
	// edit costs the configuration shapes it changed rather than every shape
	// in the fleet (verdicts.go).
	verdicts *verdictCache

	// snap is the copy-on-write read snapshot: the fleet document and its
	// settings vocabulary (catalog.json, ADR 0005) from the same working
	// tree state, swapped as ONE pointer so readers can never observe a
	// fleet from one revision joined with a catalog from another. Readers
	// must treat both as immutable.
	snap atomic.Pointer[configSnapshot]
}

// configSnapshot pairs the fleet document with the catalog, hardware
// profiles and settings profiles it shipped with (same working-tree
// revision).
type configSnapshot struct {
	fleet    *fleet.Fleet
	catalog  *fleet.Catalog
	hardware *fleet.HardwareProfiles
	profiles *fleet.Profiles
	bundles  *fleet.Bundles
}

// NewConfigService loads the initial snapshot and returns the service.
func NewConfigService(repo ports.ConfigRepo, gate ports.Gate) (*ConfigService, error) {
	s := &ConfigService{repo: repo, gate: gate, verdicts: newVerdictCache()}
	if err := s.reload(); err != nil {
		return nil, fmt.Errorf("load %s: %w", FleetFile, err)
	}
	return s, nil
}

// Fleet returns the current read snapshot. The returned document is shared
// and immutable; mutate only through Apply.
func (s *ConfigService) Fleet() *fleet.Fleet { return s.snap.Load().fleet }

// Head returns the current HEAD revision of the config repo - the revision a
// rollout ships. Empty string when the repo cannot report it (Head lives on
// the branch/ref side of the adapter, so type-assert rather than widen the
// ConfigRepo port).
func (s *ConfigService) Head(ctx context.Context) string {
	h, ok := s.repo.(interface {
		Head(context.Context) (string, error)
	})
	if !ok {
		return ""
	}
	rev, err := h.Head(ctx)
	if err != nil {
		return ""
	}
	return rev
}

// ReleaseNumber maps a revision to its human release number: the count of
// commits reachable from it. On one lineage that is monotonic, so "release
// 142 vs 145" answers newer-or-older where sha prefixes cannot (Bram's
// Intune/NetBird versioning ask). Zero when unknown; results are cached
// because a revision's count never changes.
func (s *ConfigService) ReleaseNumber(ctx context.Context, rev string) int {
	if rev == "" {
		return 0
	}
	if n, ok := s.relCache.Load(rev); ok {
		return n.(int)
	}
	c, ok := s.repo.(interface {
		CommitCount(context.Context, string) (int, error)
	})
	if !ok {
		return 0
	}
	n, err := c.CommitCount(ctx, rev)
	if err != nil {
		return 0 // unknown rev (not fetched yet); do not cache failures
	}
	s.relCache.Store(rev, n)
	return n
}

// Catalog returns the settings vocabulary snapshot (never nil).
func (s *ConfigService) Catalog() *fleet.Catalog { return s.snap.Load().catalog }

// HardwareProfiles returns the hardware-profile catalog snapshot (never nil).
func (s *ConfigService) HardwareProfiles() *fleet.HardwareProfiles {
	return s.snap.Load().hardware
}

// Profiles returns the recommended-settings profile snapshot (never nil).
func (s *ConfigService) Profiles() *fleet.Profiles { return s.snap.Load().profiles }

// Bundles returns the capability-bundle snapshot (never nil).
func (s *ConfigService) Bundles() *fleet.Bundles { return s.snap.Load().bundles }

// Snapshot returns fleet and catalog from the same revision. Handlers that
// join the two must use this, not separate Fleet()/Catalog() calls, or a
// concurrent reload could hand them mismatched halves.
func (s *ConfigService) Snapshot() (*fleet.Fleet, *fleet.Catalog) {
	sn := s.snap.Load()
	return sn.fleet, sn.catalog
}

// reload re-reads fleet.json and catalog.json from the working tree into
// the snapshot. Callers that need the fresh fleet read it back via Fleet()
// or Snapshot() - reload's job is only to publish the new snapshot.
func (s *ConfigService) reload() error {
	raw, err := s.repo.ReadFile(FleetFile)
	if err != nil {
		return err
	}
	f, err := fleet.Decode(raw)
	if err != nil {
		return err
	}
	// A missing catalog is a valid state (overlay predates the export); a
	// malformed one is not - the UI would silently lose its vocabulary.
	craw, err := s.repo.ReadFile(fleet.CatalogFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	cat, err := fleet.ParseCatalog(craw)
	if err != nil {
		return err
	}
	// hardware-profiles.json is optional the same way: a missing file is a
	// valid overlay that predates the imaging surface; a malformed one is not.
	hraw, err := s.repo.ReadFile(fleet.HardwareProfilesFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	hw, err := fleet.ParseHardwareProfiles(hraw)
	if err != nil {
		return err
	}
	// profiles.json too: an overlay without recommended-settings profiles is
	// valid, the console simply offers none.
	praw, err := s.repo.ReadFile(fleet.ProfilesFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	prof, err := fleet.ParseProfiles(praw)
	if err != nil {
		return err
	}
	// bundles.json too: an overlay without capability bundles is valid.
	braw, err := s.repo.ReadFile(fleet.BundlesFile)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	bundles, err := fleet.ParseBundles(braw)
	if err != nil {
		return err
	}
	s.snap.Store(&configSnapshot{fleet: f, catalog: cat, hardware: hw, profiles: prof, bundles: bundles})
	return nil
}

// Apply runs the safe write transaction: load -> mutate -> gate -> commit
// (-> push, with rebase-retry on a lost race). On gate rejection the working
// file is restored and a *ports.ValidationError returned; nothing invalid
// ever reaches git. affectedHosts scopes the gate to the change's blast
// radius; empty validates the whole set.
func (s *ConfigService) Apply(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author, affectedHosts ...string) error {
	return s.applyWithGate(ctx, mut, msg, a, affectedHosts, s.gate)
}

// ApplyStructural applies a change that alters no device's generated config -
// the group tree (adding/removing a group), access (RBAC) bindings, or the
// governance controls - and therefore SKIPS the nix eval, which would only
// re-evaluate byte-identical device toplevels. Structural validity is still
// enforced: the mutation validates its own invariants (slug, parent, cycle,
// emptiness) and applyTx re-decodes the document. NEVER use it for a change that
// can alter a device's config (settings, policy assignment, group re-parenting,
// the rollout plan): those must pass the gate.
func (s *ConfigService) ApplyStructural(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author) error {
	return s.applyWithGate(ctx, mut, msg, a, nil, passGate{})
}

// applyWithGate runs the safe write transaction (mutate -> gate -> commit, with
// a sync/rebase-retry loop when the repo has a remote) under a chosen gate.
func (s *ConfigService) applyWithGate(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author, affectedHosts []string, gate ports.Gate) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	if !s.repo.HasRemote() {
		return s.applyOnce(ctx, mut, msg, a, affectedHosts, gate)
	}

	// HA path: sync to the remote, apply on the fresh base, push. On a lost
	// race the mutation re-runs on the new base - clean linear history.
	var lastErr error
	for i := 0; i < maxPushRetries; i++ {
		if err := s.repo.Sync(ctx); err != nil {
			return err // remote unreachable is not a conflict: fail fast
		}
		if err := s.reload(); err != nil {
			return err
		}
		if err := s.applyOnce(ctx, mut, msg, a, affectedHosts, gate); err != nil {
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

// passGate accepts everything: the gate for ApplyStructural, where the change
// touches no device build so the nix eval has nothing to reject.
type passGate struct{}

func (passGate) Validate(context.Context, string, []string) error { return nil }

// applyOnce runs the shared transaction against the service repo and
// refreshes the snapshot on success.
func (s *ConfigService) applyOnce(ctx context.Context, mut fleet.Mutation, msg string, a ports.Author, hosts []string, gate ports.Gate) error {
	f, err := applyTx(ctx, s.repo, gate, mut, msg, a, hosts, s.verdicts)
	if err != nil {
		return err
	}
	// A write only touches fleet.json; the catalog, hardware profiles and
	// settings profiles ride along unchanged (separate overlay files).
	prev := s.snap.Load()
	s.snap.Store(&configSnapshot{fleet: f, catalog: prev.catalog, hardware: prev.hardware, profiles: prev.profiles, bundles: prev.bundles})
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
				err = s.reload()
			}
			s.writeMu.Unlock()
			if err != nil && ctx.Err() == nil {
				log.Warn("remote sync failed; keeping last good snapshot", "err", err)
			}
		}
	}
}

// applyTx is the core write transaction, shared by direct writes and
// change-request edits (which run it against a worktree repo):
// load -> mutate -> write -> gate -> commit. Gate failure rolls the working
// file back to its original bytes; nothing invalid ever gets committed.
func applyTx(ctx context.Context, repo ports.ConfigRepo, gate ports.Gate, mut fleet.Mutation, msg string, a ports.Author, hosts []string, verdicts *verdictCache) (*fleet.Fleet, error) {
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
	// Interactive validation samples one host per configuration shape instead
	// of evaluating every affected device: an option/type/assertion error
	// fails every device of a shape identically, so the sample proves the
	// change against each distinct shape. A single NixOS toplevel eval costs
	// ~10s, so this is the difference between a group re-parent of N devices
	// costing N*10s and costing shapes*10s. An unbounded (org-wide) radius
	// samples the whole fleet; a scoped radius samples within its own set.
	// The full per-host proof still happens down the pipeline: a ring's
	// release realises every member's toplevel before its branch moves
	// (build-before-promote), and a device's own rebuild converges
	// generation-safe regardless. See docs/architecture/scale.md.
	if len(hosts) == 0 {
		hosts = f.Representatives()
	} else {
		hosts = f.SampleHosts(hosts)
	}
	// Second narrowing: drop the shapes already proved clean against this
	// exact source and these exact globals (verdicts.go). An edit then costs
	// the shapes it changed, not every shape in the fleet.
	todo, keys := partitionHosts(ctx, repo, verdicts, f, hosts)
	// An empty host list means "discover and evaluate the whole fleet" to the
	// gate, so a fully-memoised sample must skip the call outright rather than
	// hand it a list that reads as everything. Only a sample that HAD hosts
	// can be skipped: an empty sample is the pre-existing whole-fleet path.
	if len(hosts) > 0 && len(todo) == 0 {
		if err := repo.Commit(ctx, msg, a, FleetFile); err != nil {
			return nil, err
		}
		return f, nil
	}
	if err := gate.Validate(ctx, repo.Dir(), todo); err != nil {
		if werr := repo.WriteFile(FleetFile, orig); werr != nil {
			// The working tree now holds the rejected edit, which would
			// become the base of the next write. Surface it loudly.
			return nil, errors.Join(err, fmt.Errorf("ROLLBACK FAILED, working tree dirty: %w", werr))
		}
		return nil, err
	}
	// Only now, with the gate's acceptance in hand, are these shapes proved.
	verdicts.record(keys)
	if err := repo.Commit(ctx, msg, a, FleetFile); err != nil {
		return nil, err
	}
	return f, nil
}
