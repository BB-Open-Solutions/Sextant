package postgres

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

// smtp.go implements ports.MailConfigStore: one SMTP configuration per tenant.
// The password column holds either a reference name or a sealed blob; this
// store keeps whichever was given opaque and never logs it.

// GetMailConfig returns the tenant's SMTP config, if any.
func (s *Store) GetMailConfig(ctx context.Context, tenant string) (mail.Config, bool, error) {
	var c mail.Config
	var sec string
	err := s.pool.QueryRow(ctx, `
		SELECT host, port, mail_from, username, password_ref, password_enc, security
		FROM smtp_config WHERE tenant = $1`, tenant).
		Scan(&c.Host, &c.Port, &c.From, &c.Username, &c.PasswordRef, &c.PasswordEnc, &sec)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return mail.Config{}, false, nil
		}
		return mail.Config{}, false, err
	}
	c.Security = mail.Security(sec)
	return c, true, nil
}

// PutMailConfig upserts the tenant's SMTP config.
func (s *Store) PutMailConfig(ctx context.Context, tenant string, c mail.Config) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO smtp_config (tenant, host, port, mail_from, username, password_ref, password_enc, security, updated)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant) DO UPDATE SET
			host = EXCLUDED.host, port = EXCLUDED.port, mail_from = EXCLUDED.mail_from,
			username = EXCLUDED.username, password_ref = EXCLUDED.password_ref,
			password_enc = EXCLUDED.password_enc, security = EXCLUDED.security,
			updated = EXCLUDED.updated`,
		tenant, c.Host, c.Port, c.From, c.Username, c.PasswordRef, c.PasswordEnc,
		string(c.Security), time.Now().UTC())
	return err
}

// DeleteMailConfig removes the tenant's SMTP config.
func (s *Store) DeleteMailConfig(ctx context.Context, tenant string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM smtp_config WHERE tenant = $1`, tenant)
	return err
}
