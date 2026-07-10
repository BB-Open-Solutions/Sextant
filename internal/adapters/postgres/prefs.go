package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// prefs.go implements ports.PrefsStore: per-user presentation preferences.

// GetPrefs returns a user's stored preferences.
func (s *Store) GetPrefs(ctx context.Context, tenant, subject string) (identity.Preferences, bool, error) {
	var p identity.Preferences
	err := s.pool.QueryRow(ctx,
		`SELECT timezone, locale FROM user_prefs WHERE tenant = $1 AND subject = $2`,
		tenant, subject).Scan(&p.Timezone, &p.Locale)
	if err != nil {
		if err == pgx.ErrNoRows {
			return identity.Preferences{}, false, nil
		}
		return identity.Preferences{}, false, err
	}
	return p, true, nil
}

// PutPrefs upserts a user's preferences.
func (s *Store) PutPrefs(ctx context.Context, tenant, subject string, p identity.Preferences, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO user_prefs (tenant, subject, timezone, locale, updated)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (tenant, subject) DO UPDATE
		SET timezone = EXCLUDED.timezone, locale = EXCLUDED.locale,
		    updated = EXCLUDED.updated`,
		tenant, subject, p.Timezone, p.Locale, now)
	return err
}
