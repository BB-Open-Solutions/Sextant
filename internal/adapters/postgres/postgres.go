package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

// Store implements the observed-plane ports on one pgx pool.
type Store struct {
	pool *pgxpool.Pool
}

// Open connects, migrates and returns the store. The pool is sized by the
// DSN (pool_max_conns) or pgx defaults.
func Open(ctx context.Context, dsn string) (*Store, error) {
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres connect: %w", err)
	}
	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	return &Store{pool: pool}, nil
}

// Close releases the pool.
func (s *Store) Close() { s.pool.Close() }

// Ping implements ports.StatusStore (deep readiness).
func (s *Store) Ping(ctx context.Context) error { return s.pool.Ping(ctx) }

// Upsert implements ports.StatusStore: one write per check-in, keyed
// (tenant, tag). Empty phase/revision in a check-in keeps the stored value
// (a light heartbeat never erases richer state).
func (s *Store) Upsert(ctx context.Context, tenant string, c observed.CheckIn, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_status (tenant, tag, revision, phase, error, last_seen, sb_state, tpm2_state)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (tenant, tag) DO UPDATE SET
			revision   = CASE WHEN EXCLUDED.revision = ''   THEN device_status.revision   ELSE EXCLUDED.revision   END,
			phase      = CASE WHEN EXCLUDED.phase = ''       THEN device_status.phase      ELSE EXCLUDED.phase      END,
			error      = EXCLUDED.error,
			last_seen  = EXCLUDED.last_seen,
			sb_state   = CASE WHEN EXCLUDED.sb_state = ''    THEN device_status.sb_state   ELSE EXCLUDED.sb_state   END,
			tpm2_state = CASE WHEN EXCLUDED.tpm2_state = ''  THEN device_status.tpm2_state ELSE EXCLUDED.tpm2_state END`,
		tenant, c.Tag, c.Revision, string(c.Phase), c.Error, now, string(c.SB), string(c.TPM2))
	return err
}

// Get implements ports.StatusStore.
func (s *Store) Get(ctx context.Context, tenant, tag string) (observed.DeviceStatus, bool, error) {
	var st observed.DeviceStatus
	var phase, sb, tpm2 string
	err := s.pool.QueryRow(ctx, `
		SELECT tag, revision, phase, error, last_seen, sb_state, tpm2_state
		FROM device_status WHERE tenant = $1 AND tag = $2`, tenant, tag).
		Scan(&st.Tag, &st.Revision, &phase, &st.Error, &st.LastSeen, &sb, &tpm2)
	if err == pgx.ErrNoRows {
		return observed.DeviceStatus{}, false, nil
	}
	if err != nil {
		return observed.DeviceStatus{}, false, err
	}
	st.Phase = observed.Phase(phase)
	st.SB, st.TPM2 = observed.SBState(sb), observed.TPM2State(tpm2)
	return st, true, nil
}

// List implements ports.StatusStore.
func (s *Store) List(ctx context.Context, tenant string) ([]observed.DeviceStatus, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT tag, revision, phase, error, last_seen, sb_state, tpm2_state
		FROM device_status WHERE tenant = $1 ORDER BY tag`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []observed.DeviceStatus
	for rows.Next() {
		var st observed.DeviceStatus
		var phase, sb, tpm2 string
		if err := rows.Scan(&st.Tag, &st.Revision, &phase, &st.Error, &st.LastSeen, &sb, &tpm2); err != nil {
			return nil, err
		}
		st.Phase = observed.Phase(phase)
		st.SB, st.TPM2 = observed.SBState(sb), observed.TPM2State(tpm2)
		out = append(out, st)
	}
	return out, rows.Err()
}

// PutFacts implements ports.InventoryStore.
func (s *Store) PutFacts(ctx context.Context, tenant, tag string, facts []byte, now time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_facts (tenant, tag, facts, updated_at)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (tenant, tag) DO UPDATE SET
			facts = EXCLUDED.facts, updated_at = EXCLUDED.updated_at`,
		tenant, tag, facts, now)
	return err
}

// GetFacts implements ports.InventoryStore.
func (s *Store) GetFacts(ctx context.Context, tenant, tag string) ([]byte, time.Time, bool, error) {
	var facts []byte
	var at time.Time
	err := s.pool.QueryRow(ctx,
		"SELECT facts, updated_at FROM device_facts WHERE tenant = $1 AND tag = $2",
		tenant, tag).Scan(&facts, &at)
	if err == pgx.ErrNoRows {
		return nil, time.Time{}, false, nil
	}
	if err != nil {
		return nil, time.Time{}, false, err
	}
	return facts, at, true, nil
}

// Convergence builds a ports.ConvergenceSource for one tenant. tags lists
// the devices of a ring's group (from the config plane); the aggregate runs
// in SQL - application code never iterates devices.
type Convergence struct {
	store  *Store
	tenant string
	// Tags resolves a group to its device tags from the config snapshot.
	Tags func(group string) []string
	// Now is injectable for tests; nil uses time.Now.
	Now func() time.Time
}

// NewConvergence wires the convergence source.
func (s *Store) NewConvergence(tenant string, tags func(group string) []string) *Convergence {
	return &Convergence{store: s, tenant: tenant, Tags: tags}
}

// RingStatus implements ports.ConvergenceSource with one aggregate query.
func (c *Convergence) RingStatus(ctx context.Context, group, target string) (rollout.RingStatus, error) {
	tags := c.Tags(group)
	if len(tags) == 0 {
		return rollout.RingStatus{}, nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	cutoff := now().Add(-observed.OnlineWindow)

	var rs rollout.RingStatus
	// Total counts the ring's devices (config is truth for membership);
	// on-target and healthy come from observed rows.
	rs.Total = len(tags)
	err := c.store.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE revision = $3),
			COUNT(*) FILTER (WHERE revision = $3
				AND last_seen >= $4
				AND (phase = '' OR phase = 'running')
				AND error = '')
		FROM device_status
		WHERE tenant = $1 AND tag = ANY($2)`,
		c.tenant, tags, target, cutoff).Scan(&rs.OnTarget, &rs.Healthy)
	if err != nil {
		return rollout.RingStatus{}, err
	}
	return rs, nil
}
