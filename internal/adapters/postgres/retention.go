package postgres

import (
	"context"
	"time"
)

// retention.go implements ports.RetentionStore. Every statement is a bounded
// DELETE with an explicit tenant and cutoff; none of them can remove
// everything if a parameter arrives empty, which is the failure mode that
// makes a retention sweep dangerous rather than tidy.

// DeleteNotificationsBefore removes notifications older than cutoff, and the
// read markers that point at them - an orphaned marker is a row nobody can
// ever reach.
func (s *Store) DeleteNotificationsBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error) {
	// The read markers go first, and only those pointing at rows this call is
	// about to remove. Deleting the notification alone would leave a marker
	// nothing can ever reach again - a row that grows forever and belongs to
	// a person.
	if _, err := s.pool.Exec(ctx, `
		DELETE FROM notification_reads r
		WHERE r.tenant = $1
		  AND EXISTS (
			SELECT 1 FROM notifications n
			WHERE n.tenant = r.tenant AND n.id = r.notif_id AND n.created < $2)`,
		tenant, cutoff); err != nil {
		return 0, err
	}
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM notifications WHERE tenant = $1 AND created < $2`, tenant, cutoff)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// DeleteElevationBefore removes elevation requests created before cutoff.
func (s *Store) DeleteElevationBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM elevation_requests WHERE tenant = $1 AND created < $2`, tenant, cutoff)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// DeleteSeenUsersBefore removes cached operator identities not seen since
// cutoff. The cache is a convenience - the directory remains the source of
// truth - so losing a row costs one lookup, not a fact.
func (s *Store) DeleteSeenUsersBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error) {
	tag, err := s.pool.Exec(ctx,
		`DELETE FROM seen_users WHERE tenant = $1 AND seen < $2`, tenant, cutoff)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}

// DeleteDeviceStatusBefore removes check-ins for tags the fleet no longer
// has AND that have been silent since cutoff.
//
// The empty-known guard is not defensive noise: with an empty set every tag
// looks forgotten, so a fleet document that failed to load would erase the
// observed plane. The caller refuses that case too; this refuses it again,
// because the cost of being wrong here is the whole table.
func (s *Store) DeleteDeviceStatusBefore(ctx context.Context, tenant string, cutoff time.Time, known map[string]bool) (int, error) {
	if len(known) == 0 {
		return 0, nil
	}
	tags := make([]string, 0, len(known))
	for t := range known {
		tags = append(tags, t)
	}
	tag, err := s.pool.Exec(ctx, `
		DELETE FROM device_status
		WHERE tenant = $1 AND last_seen < $2 AND NOT (tag = ANY($3))`,
		tenant, cutoff, tags)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
