package postgres

import (
	"context"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// notify.go implements ports.NotifyStore: in-app notifications with per-reader
// read state. A reader sees a notification when it is addressed to their
// subject or to one of their memberships; whether they have read it is a row
// in notification_reads, so a broadcast is read independently per person.

// Add stores one notification (idempotent on id).
func (s *Store) Add(ctx context.Context, n notify.Notification) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notifications (id, tenant, recipient, audience, kind, title, body, link, created)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
		ON CONFLICT (tenant, id) DO NOTHING`,
		n.ID, n.Tenant, n.Recipient, n.Audience, string(n.Kind), n.Title, n.Body, n.Link, n.CreatedAt)
	return err
}

// ListFor returns the newest notifications a reader should see, each carrying
// its per-reader Read flag.
func (s *Store) ListFor(ctx context.Context, tenant, subject string, memberships []string, limit int) ([]notify.Notification, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
		SELECT n.id, n.recipient, n.audience, n.kind, n.title, n.body, n.link, n.created,
		       (r.subject IS NOT NULL) AS read
		FROM notifications n
		LEFT JOIN notification_reads r
		  ON r.tenant = n.tenant AND r.notif_id = n.id AND r.subject = $2
		WHERE n.tenant = $1 AND (n.recipient = $2 OR n.audience = ANY($3))
		ORDER BY n.created DESC
		LIMIT $4`, tenant, subject, memberships, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []notify.Notification
	for rows.Next() {
		n := notify.Notification{Tenant: tenant}
		var kind string
		if err := rows.Scan(&n.ID, &n.Recipient, &n.Audience, &kind,
			&n.Title, &n.Body, &n.Link, &n.CreatedAt, &n.Read); err != nil {
			return nil, err
		}
		n.Kind = notify.Kind(kind)
		out = append(out, n)
	}
	return out, rows.Err()
}

// UnreadCount counts the reader's notifications with no read record.
func (s *Store) UnreadCount(ctx context.Context, tenant, subject string, memberships []string) (int, error) {
	var c int
	err := s.pool.QueryRow(ctx, `
		SELECT count(*) FROM notifications n
		WHERE n.tenant = $1 AND (n.recipient = $2 OR n.audience = ANY($3))
		  AND NOT EXISTS (
		      SELECT 1 FROM notification_reads r
		      WHERE r.tenant = n.tenant AND r.notif_id = n.id AND r.subject = $2)`,
		tenant, subject, memberships).Scan(&c)
	return c, err
}

// MarkRead records that this reader read one notification.
func (s *Store) MarkRead(ctx context.Context, tenant, subject, id string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_reads (tenant, notif_id, subject, read_at)
		VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING`,
		tenant, id, subject, time.Now().UTC())
	return err
}

// MarkAllRead marks every notification the reader can see as read.
func (s *Store) MarkAllRead(ctx context.Context, tenant, subject string, memberships []string) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO notification_reads (tenant, notif_id, subject, read_at)
		SELECT n.tenant, n.id, $2, $4 FROM notifications n
		WHERE n.tenant = $1 AND (n.recipient = $2 OR n.audience = ANY($3))
		ON CONFLICT DO NOTHING`,
		tenant, subject, memberships, time.Now().UTC())
	return err
}
