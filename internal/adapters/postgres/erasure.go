package postgres

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// erasure.go implements ports.ErasureStore.
//
// Every statement below requires a non-empty identifier. That is not
// defensive noise: an empty subject would match every row whose subject
// column is empty - and notifications addressed to a GROUP have exactly
// that shape. An erasure run with a blank field would quietly delete the
// organisation's group notifications and report it as erasing one person.

// CountPersonalData reports what is held without removing it. This is the
// preview an operator sees before confirming, so it must count exactly what
// ErasePersonalData would remove - the two queries are deliberately written
// against the same predicates.
func (s *Store) CountPersonalData(ctx context.Context, tenant, subject, username string) (ports.PersonalDataCounts, error) {
	var c ports.PersonalDataCounts
	if subject != "" {
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM seen_users WHERE tenant = $1 AND subject = $2`,
			tenant, subject).Scan(&c.SeenUser); err != nil {
			return c, err
		}
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM user_prefs WHERE tenant = $1 AND subject = $2`,
			tenant, subject).Scan(&c.Prefs); err != nil {
			return c, err
		}
		// recipient <> '' keeps group notifications out: those are addressed
		// to an audience, not to this person, and are not theirs to erase.
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM notifications
			 WHERE tenant = $1 AND recipient = $2 AND recipient <> ''`,
			tenant, subject).Scan(&c.Notifications); err != nil {
			return c, err
		}
	}
	if username != "" {
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM elevation_requests WHERE tenant = $1 AND "user" = $2`,
			tenant, username).Scan(&c.Elevation); err != nil {
			return c, err
		}
		if err := s.pool.QueryRow(ctx,
			`SELECT count(*) FROM elevation_requests
			 WHERE tenant = $1 AND decided_by = $2 AND "user" <> $2`,
			tenant, username).Scan(&c.ElevationDecided); err != nil {
			return c, err
		}
	}
	return c, nil
}

// ErasePersonalData removes the rows CountPersonalData counted.
//
// Requests this person DECIDED are deliberately left: they are somebody
// else's record of who approved their access, and erasing them on this
// person's request would destroy another data subject's evidence.
func (s *Store) ErasePersonalData(ctx context.Context, tenant, subject, username string) (ports.PersonalDataCounts, error) {
	var c ports.PersonalDataCounts
	if subject != "" {
		// Read markers first: a marker pointing at a deleted notification is
		// a row nobody can reach and that still names a person.
		if _, err := s.pool.Exec(ctx,
			`DELETE FROM notification_reads WHERE tenant = $1 AND subject = $2`,
			tenant, subject); err != nil {
			return c, err
		}
		tag, err := s.pool.Exec(ctx,
			`DELETE FROM notifications
			 WHERE tenant = $1 AND recipient = $2 AND recipient <> ''`, tenant, subject)
		if err != nil {
			return c, err
		}
		c.Notifications = int(tag.RowsAffected())

		if tag, err = s.pool.Exec(ctx,
			`DELETE FROM user_prefs WHERE tenant = $1 AND subject = $2`, tenant, subject); err != nil {
			return c, err
		}
		c.Prefs = int(tag.RowsAffected())

		if tag, err = s.pool.Exec(ctx,
			`DELETE FROM seen_users WHERE tenant = $1 AND subject = $2`, tenant, subject); err != nil {
			return c, err
		}
		c.SeenUser = int(tag.RowsAffected())
	}
	if username != "" {
		tag, err := s.pool.Exec(ctx,
			`DELETE FROM elevation_requests WHERE tenant = $1 AND "user" = $2`, tenant, username)
		if err != nil {
			return c, err
		}
		c.Elevation = int(tag.RowsAffected())
	}
	return c, nil
}
