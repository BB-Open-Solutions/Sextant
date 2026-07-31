package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
)

// elevation.go: the elevation-request queue (#27). Deliberately dumb - it
// stores facts (created, decided, by whom) and never a computed state. Whether
// a request is still pending depends on the clock, so the domain works that
// out and the table cannot go stale.

// Elevation exposes the request store on the pool.
func (s *Store) Elevation() *ElevationStore { return &ElevationStore{s} }

// ElevationStore implements ports.ElevationStore.
type ElevationStore struct{ s *Store }

// Put writes a request or replaces it with its decided form.
func (e *ElevationStore) Put(ctx context.Context, tenant string, r elevation.Request) error {
	var approved *bool
	var decidedAt *time.Time
	switch r.State {
	case elevation.Approved, elevation.Denied:
		yes := r.State == elevation.Approved
		approved = &yes
		d := r.Decided
		decidedAt = &d
	}
	_, err := e.s.pool.Exec(ctx, `
		INSERT INTO elevation_requests
			(tenant, id, tag, "user", action, reason, approved, created, decided_at, decided_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		ON CONFLICT (tenant, id) DO UPDATE SET
			approved=EXCLUDED.approved,
			decided_at=EXCLUDED.decided_at,
			decided_by=EXCLUDED.decided_by`,
		tenant, r.ID, r.Tag, r.User, r.Action, r.Reason, approved, r.Created, decidedAt, r.DecidedBy)
	if err != nil {
		return fmt.Errorf("put elevation request %s: %w", r.ID, err)
	}
	return nil
}

// Get returns one request by id.
func (e *ElevationStore) Get(ctx context.Context, tenant, id string) (elevation.Request, bool, error) {
	row := e.s.pool.QueryRow(ctx, `
		SELECT id, tag, "user", action, reason, approved, created, decided_at, decided_by
		FROM elevation_requests WHERE tenant=$1 AND id=$2`, tenant, id)
	r, err := scanElevation(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return elevation.Request{}, false, nil
	}
	if err != nil {
		return elevation.Request{}, false, fmt.Errorf("get elevation request %s: %w", id, err)
	}
	return r, true, nil
}

// Pending returns the requests nobody has answered, oldest first: somebody is
// standing in front of each of them, and the one who has waited longest is
// closest to giving up.
//
// Undecided, not unexpired. Expiry is the caller's to apply - the row does not
// change when the window passes, so a store that tried to filter on time here
// would be guessing at a clock it does not own.
func (e *ElevationStore) Pending(ctx context.Context, tenant string) ([]elevation.Request, error) {
	rows, err := e.s.pool.Query(ctx, `
		SELECT id, tag, "user", action, reason, approved, created, decided_at, decided_by
		FROM elevation_requests
		WHERE tenant=$1 AND decided_at IS NULL
		ORDER BY created ASC`, tenant)
	if err != nil {
		return nil, fmt.Errorf("list pending elevation requests: %w", err)
	}
	defer rows.Close()
	var out []elevation.Request
	for rows.Next() {
		r, err := scanElevation(rows)
		if err != nil {
			return nil, fmt.Errorf("scan elevation request: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// Prune deletes requests older than keep. This table holds a queue, not a log:
// a request is dead five minutes after it is created, so rows that survive
// beyond the audit window are only cost. The audit trail of who approved what
// lives in the audit log, which is built to be kept.
func (e *ElevationStore) Prune(ctx context.Context, before time.Time) (int64, error) {
	tag, err := e.s.pool.Exec(ctx,
		`DELETE FROM elevation_requests WHERE created < $1`, before)
	if err != nil {
		return 0, fmt.Errorf("prune elevation requests: %w", err)
	}
	return tag.RowsAffected(), nil
}

type scannable interface{ Scan(dest ...any) error }

func scanElevation(row scannable) (elevation.Request, error) {
	var (
		r         elevation.Request
		approved  *bool
		decidedAt *time.Time
	)
	if err := row.Scan(&r.ID, &r.Tag, &r.User, &r.Action, &r.Reason,
		&approved, &r.Created, &decidedAt, &r.DecidedBy); err != nil {
		return elevation.Request{}, err
	}
	// State is reconstructed from the facts rather than read back, so there is
	// exactly one place that decides what a row means.
	r.State = elevation.Pending
	if decidedAt != nil {
		r.Decided = *decidedAt
		r.State = elevation.Denied
		if approved != nil && *approved {
			r.State = elevation.Approved
		}
	}
	return r, nil
}
