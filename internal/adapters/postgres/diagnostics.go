package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// diagnostics.go: per-device diagnostics bundles sealed at rest (design
// 0010). One row per device, replaced on re-request; the application layer
// seals/opens and enforces retention.

// Diagnostics exposes the bundle store on the pool.
func (s *Store) Diagnostics() *DiagnosticsStore { return &DiagnosticsStore{s} }

// DiagnosticsStore implements ports.DiagnosticsStore.
type DiagnosticsStore struct{ s *Store }

// Put stores or replaces the device's sealed bundle.
func (d *DiagnosticsStore) Put(ctx context.Context, tenant, tag string, ciphertext []byte, now time.Time) error {
	_, err := d.s.pool.Exec(ctx, `
		INSERT INTO device_diagnostics (tenant, tag, ciphertext, created)
		VALUES ($1,$2,$3,$4)
		ON CONFLICT (tenant, tag) DO UPDATE SET
			ciphertext=EXCLUDED.ciphertext, created=EXCLUDED.created`,
		tenant, tag, ciphertext, now)
	if err != nil {
		return fmt.Errorf("put diagnostics bundle %s: %w", tag, err)
	}
	return nil
}

// Get returns the sealed bundle and metadata.
func (d *DiagnosticsStore) Get(ctx context.Context, tenant, tag string) ([]byte, ports.DiagnosticsMeta, bool, error) {
	var (
		ciphertext []byte
		created    time.Time
	)
	err := d.s.pool.QueryRow(ctx, `
		SELECT ciphertext, created FROM device_diagnostics
		WHERE tenant=$1 AND tag=$2`, tenant, tag).Scan(&ciphertext, &created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ports.DiagnosticsMeta{}, false, nil
		}
		return nil, ports.DiagnosticsMeta{}, false, err
	}
	return ciphertext, ports.DiagnosticsMeta{Tag: tag, Size: len(ciphertext), Created: created}, true, nil
}

// Meta returns only the metadata (the page render never moves the bundle).
func (d *DiagnosticsStore) Meta(ctx context.Context, tenant, tag string) (ports.DiagnosticsMeta, bool, error) {
	var (
		size    int
		created time.Time
	)
	err := d.s.pool.QueryRow(ctx, `
		SELECT length(ciphertext), created FROM device_diagnostics
		WHERE tenant=$1 AND tag=$2`, tenant, tag).Scan(&size, &created)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ports.DiagnosticsMeta{}, false, nil
		}
		return ports.DiagnosticsMeta{}, false, err
	}
	return ports.DiagnosticsMeta{Tag: tag, Size: size, Created: created}, true, nil
}

// Delete removes the device's bundle.
func (d *DiagnosticsStore) Delete(ctx context.Context, tenant, tag string) error {
	_, err := d.s.pool.Exec(ctx,
		`DELETE FROM device_diagnostics WHERE tenant=$1 AND tag=$2`, tenant, tag)
	return err
}
