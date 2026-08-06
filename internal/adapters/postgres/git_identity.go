package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
)

// git_identity.go implements ports.ForgeIdentityStore: the console's own forge
// credential, one row per tenant. The token column is a sealed blob; this
// store keeps it opaque and never logs it.

// GetForgeIdentity returns the tenant's forge identity, if any.
func (s *Store) GetForgeIdentity(ctx context.Context, tenant string) (forge.Identity, bool, error) {
	var id forge.Identity
	err := s.pool.QueryRow(ctx, `
		SELECT host, username, token_enc, updated, updated_by
		FROM git_identity WHERE tenant = $1`, tenant).
		Scan(&id.Host, &id.Username, &id.TokenEnc, &id.Updated, &id.UpdatedBy)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return forge.Identity{}, false, nil
		}
		return forge.Identity{}, false, err
	}
	return id, true, nil
}

// PutForgeIdentity upserts the tenant's forge identity.
func (s *Store) PutForgeIdentity(ctx context.Context, tenant string, id forge.Identity) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO git_identity (tenant, host, username, token_enc, updated, updated_by)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant) DO UPDATE SET
			host = EXCLUDED.host, username = EXCLUDED.username,
			token_enc = EXCLUDED.token_enc, updated = EXCLUDED.updated,
			updated_by = EXCLUDED.updated_by`,
		tenant, id.Host, id.Username, id.TokenEnc, time.Now().UTC(), id.UpdatedBy)
	return err
}

// DeleteForgeIdentity removes the tenant's forge identity, which returns the
// deployment to whatever credential is mounted.
func (s *Store) DeleteForgeIdentity(ctx context.Context, tenant string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM git_identity WHERE tenant = $1`, tenant)
	return err
}
