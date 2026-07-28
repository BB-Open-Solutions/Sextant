package app

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// diagnostics.go: design 0010 - operator-requested, bounded diagnostics
// bundles. One bundle per device at rest, sealed with the same secretbox the
// device secrets use (journals can contain personal data), gone after the
// retention window or on retire. This service seals/opens and enforces
// retention; the store only ever holds ciphertext.

// DiagnosticsRetention is how long a bundle stays retrievable. Support
// material, not a record: two weeks covers a ticket's lifetime.
const DiagnosticsRetention = 14 * 24 * time.Hour

// MaxDiagnosticsBundle caps an uploaded bundle (gzip) - matches the station
// report cap; the device-side collector stays well under it.
const MaxDiagnosticsBundle = 4 << 20

// DiagnosticsService seals, stores and serves diagnostics bundles.
type DiagnosticsService struct {
	store  ports.DiagnosticsStore
	sealer secretbox.Sealer
	clock  ports.Clock
	tenant string
}

// NewDiagnosticsService wires the bundle store for one tenant.
func NewDiagnosticsService(store ports.DiagnosticsStore, sealer secretbox.Sealer, clock ports.Clock, tenant string) *DiagnosticsService {
	if store == nil {
		return nil
	}
	return &DiagnosticsService{store: store, sealer: sealer, clock: clock, tenant: tenant}
}

// Enabled reports whether bundles can be stored (store present, key set).
func (s *DiagnosticsService) Enabled() bool {
	return s != nil && s.store != nil && s.sealer.Enabled()
}

// Put seals and stores a device's bundle, replacing any previous one.
func (s *DiagnosticsService) Put(ctx context.Context, tag string, bundle []byte) error {
	if !s.Enabled() {
		return fmt.Errorf("diagnostics store is not configured")
	}
	if len(bundle) == 0 || len(bundle) > MaxDiagnosticsBundle {
		return fmt.Errorf("bundle size %d out of bounds", len(bundle))
	}
	sealed, err := s.sealer.Seal(bundle)
	if err != nil {
		return err
	}
	return s.store.Put(ctx, s.tenant, tag, sealed, s.clock.Now())
}

// Get opens a device's bundle. An expired bundle is deleted on sight and
// reads as absent - retention is enforced at every exit, not by a sweeper.
func (s *DiagnosticsService) Get(ctx context.Context, tag string) ([]byte, ports.DiagnosticsMeta, bool, error) {
	if !s.Enabled() {
		return nil, ports.DiagnosticsMeta{}, false, nil
	}
	sealed, meta, ok, err := s.store.Get(ctx, s.tenant, tag)
	if err != nil || !ok {
		return nil, ports.DiagnosticsMeta{}, false, err
	}
	if s.expired(meta) {
		_ = s.store.Delete(ctx, s.tenant, tag)
		return nil, ports.DiagnosticsMeta{}, false, nil
	}
	bundle, err := s.sealer.Open(sealed)
	if err != nil {
		return nil, ports.DiagnosticsMeta{}, false, err
	}
	return bundle, meta, true, nil
}

// Meta returns the bundle's metadata for display, honouring retention.
func (s *DiagnosticsService) Meta(ctx context.Context, tag string) (ports.DiagnosticsMeta, bool, error) {
	if !s.Enabled() {
		return ports.DiagnosticsMeta{}, false, nil
	}
	meta, ok, err := s.store.Meta(ctx, s.tenant, tag)
	if err != nil || !ok {
		return ports.DiagnosticsMeta{}, false, err
	}
	if s.expired(meta) {
		_ = s.store.Delete(ctx, s.tenant, tag)
		return ports.DiagnosticsMeta{}, false, nil
	}
	return meta, true, nil
}

// Delete removes a device's bundle (retire, or an operator cleaning up).
func (s *DiagnosticsService) Delete(ctx context.Context, tag string) error {
	if s == nil || s.store == nil {
		return nil
	}
	return s.store.Delete(ctx, s.tenant, tag)
}

func (s *DiagnosticsService) expired(meta ports.DiagnosticsMeta) bool {
	return s.clock.Now().Sub(meta.Created) > DiagnosticsRetention
}
