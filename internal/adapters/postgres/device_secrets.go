package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

// device_secrets.go: per-device secrets sealed at rest. The store holds only
// ciphertext (the application seals with AES-256-GCM) and the create/reveal
// provenance; it never handles plaintext.

// DeviceSecrets exposes the per-device secret store on the pool.
func (s *Store) DeviceSecrets() *DeviceSecretStore { return &DeviceSecretStore{s} }

// DeviceSecretStore implements ports.DeviceSecretStore.
type DeviceSecretStore struct{ s *Store }

// Put stores or replaces the sealed secret for one device+kind, clearing the
// revealed marker so a freshly stored value reads as never-revealed.
func (d *DeviceSecretStore) Put(ctx context.Context, tenant, tag string, kind secret.Kind, ciphertext []byte, createdBy string, now time.Time) error {
	_, err := d.s.pool.Exec(ctx, `
		INSERT INTO device_secrets (tenant, tag, kind, ciphertext, created, created_by, revealed, revealed_by)
		VALUES ($1,$2,$3,$4,$5,$6,NULL,'')
		ON CONFLICT (tenant, tag, kind) DO UPDATE SET
			ciphertext=EXCLUDED.ciphertext, created=EXCLUDED.created,
			created_by=EXCLUDED.created_by, revealed=NULL, revealed_by=''`,
		tenant, tag, string(kind), ciphertext, now, createdBy)
	if err != nil {
		return fmt.Errorf("put device secret %s/%s: %w", tag, kind, err)
	}
	return nil
}

// Get returns the sealed ciphertext and metadata for one device+kind.
func (d *DeviceSecretStore) Get(ctx context.Context, tenant, tag string, kind secret.Kind) ([]byte, secret.Meta, bool, error) {
	var (
		ciphertext []byte
		created    time.Time
		createdBy  string
		revealed   *time.Time
		revealedBy string
	)
	err := d.s.pool.QueryRow(ctx, `
		SELECT ciphertext, created, created_by, revealed, revealed_by
		FROM device_secrets WHERE tenant=$1 AND tag=$2 AND kind=$3`,
		tenant, tag, string(kind)).Scan(&ciphertext, &created, &createdBy, &revealed, &revealedBy)
	if err != nil {
		if err == pgx.ErrNoRows {
			return nil, secret.Meta{}, false, nil
		}
		return nil, secret.Meta{}, false, err
	}
	return ciphertext, metaOf(tag, kind, created, createdBy, revealed, revealedBy), true, nil
}

// List returns a device's secret metadata, never the ciphertext.
func (d *DeviceSecretStore) List(ctx context.Context, tenant, tag string) ([]secret.Meta, error) {
	rows, err := d.s.pool.Query(ctx, `
		SELECT kind, created, created_by, revealed, revealed_by
		FROM device_secrets WHERE tenant=$1 AND tag=$2 ORDER BY kind`, tenant, tag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []secret.Meta
	for rows.Next() {
		var (
			kind       string
			created    time.Time
			createdBy  string
			revealed   *time.Time
			revealedBy string
		)
		if err := rows.Scan(&kind, &created, &createdBy, &revealed, &revealedBy); err != nil {
			return nil, err
		}
		out = append(out, metaOf(tag, secret.Kind(kind), created, createdBy, revealed, revealedBy))
	}
	return out, rows.Err()
}

// MarkRevealed records who read a secret and when.
func (d *DeviceSecretStore) MarkRevealed(ctx context.Context, tenant, tag string, kind secret.Kind, revealedBy string, now time.Time) error {
	_, err := d.s.pool.Exec(ctx, `
		UPDATE device_secrets SET revealed=$4, revealed_by=$5
		WHERE tenant=$1 AND tag=$2 AND kind=$3`,
		tenant, tag, string(kind), now, revealedBy)
	return err
}

// metaOf assembles the non-secret record, formatting times as RFC3339 (empty
// when the secret has never been revealed).
func metaOf(tag string, kind secret.Kind, created time.Time, createdBy string, revealed *time.Time, revealedBy string) secret.Meta {
	m := secret.Meta{
		Tag: tag, Kind: kind,
		CreatedBy: createdBy, Created: created.UTC().Format(time.RFC3339),
		RevealedBy: revealedBy,
	}
	if revealed != nil {
		m.Revealed = revealed.UTC().Format(time.RFC3339)
	}
	return m
}
