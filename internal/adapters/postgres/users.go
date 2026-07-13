package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
)

// users.go implements ports.UserDirectory: the seen-users address book that
// lets the notifier deliver by e-mail. Rows come from the console session, so
// this is the same identity data the app already trusts for authorization.

// RecordUser upserts what a login revealed. Groups replace the prior set so a
// membership change is reflected on the next login.
func (s *Store) RecordUser(ctx context.Context, tenant, subject, email, name string, groups []string) error {
	if groups == nil {
		groups = []string{}
	}
	_, err := s.pool.Exec(ctx, `
		INSERT INTO seen_users (tenant, subject, email, name, groups, seen)
		VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (tenant, subject) DO UPDATE
		SET email = EXCLUDED.email, name = EXCLUDED.name,
		    groups = EXCLUDED.groups, seen = EXCLUDED.seen`,
		tenant, subject, email, name, groups, time.Now().UTC())
	return err
}

// EmailForSubject returns a user's e-mail. A stored empty e-mail counts as "no
// address", so the caller does not try to mail an empty string.
func (s *Store) EmailForSubject(ctx context.Context, tenant, subject string) (string, bool, error) {
	var email string
	err := s.pool.QueryRow(ctx,
		`SELECT email FROM seen_users WHERE tenant = $1 AND subject = $2`,
		tenant, subject).Scan(&email)
	if err != nil {
		if err == pgx.ErrNoRows {
			return "", false, nil
		}
		return "", false, err
	}
	if email == "" {
		return "", false, nil
	}
	return email, true, nil
}

// EmailsForAudience returns the e-mails of every seen user in a group.
func (s *Store) EmailsForAudience(ctx context.Context, tenant, group string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT email FROM seen_users WHERE tenant = $1 AND $2 = ANY(groups) AND email <> ''`,
		tenant, group)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var e string
		if err := rows.Scan(&e); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
