package postgres

import (
	"context"
	"errors"
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
//
// ackChanged is computed inside the same statement (a CTE reading the
// pre-write ack, compared against the post-write ack in RETURNING) rather
// than via a separate SELECT beforehand: a read-then-write here would let
// two concurrent check-ins for the same tag both observe the same prior ack
// and both report a change, duplicating a wipe-outcome notification for a
// security-relevant event. The CTE runs against the pre-statement snapshot
// (standard Postgres semantics for data-modifying CTEs), so this is race-free.
func (s *Store) Upsert(ctx context.Context, tenant string, c observed.CheckIn, now time.Time) (bool, error) {
	u := c.Usage
	var ackChanged bool
	err := s.pool.QueryRow(ctx, `
		WITH prev AS (
			SELECT ack FROM device_status WHERE tenant = $1 AND tag = $2
		)
		INSERT INTO device_status (tenant, tag, revision, phase, error, last_seen, sb_state, tpm2_state, ack,
			cpu_pct, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)
		ON CONFLICT (tenant, tag) DO UPDATE SET
			revision   = CASE WHEN EXCLUDED.revision = ''   THEN device_status.revision   ELSE EXCLUDED.revision   END,
			phase      = CASE WHEN EXCLUDED.phase = ''       THEN device_status.phase      ELSE EXCLUDED.phase      END,
			error      = EXCLUDED.error,
			last_seen  = EXCLUDED.last_seen,
			sb_state   = CASE WHEN EXCLUDED.sb_state = ''    THEN device_status.sb_state   ELSE EXCLUDED.sb_state   END,
			tpm2_state = CASE WHEN EXCLUDED.tpm2_state = ''  THEN device_status.tpm2_state ELSE EXCLUDED.tpm2_state END,
			ack        = CASE WHEN EXCLUDED.ack = ''         THEN device_status.ack        ELSE EXCLUDED.ack        END,
			-- Only overwrite utilisation when the beat carried a reading for
			-- that dimension, so an old agent's empty beat (or a partial one,
			-- e.g. a failed memory probe alongside a good cpu/disk read)
			-- keeps the last good figure per-dimension instead of dropping
			-- the whole row to the stale values on any single zero field.
			cpu_pct       = CASE WHEN EXCLUDED.cpu_pct = 0       THEN device_status.cpu_pct       ELSE EXCLUDED.cpu_pct       END,
			mem_used_mb   = CASE WHEN EXCLUDED.mem_total_mb = 0  THEN device_status.mem_used_mb   ELSE EXCLUDED.mem_used_mb   END,
			mem_total_mb  = CASE WHEN EXCLUDED.mem_total_mb = 0  THEN device_status.mem_total_mb  ELSE EXCLUDED.mem_total_mb  END,
			disk_used_gb  = CASE WHEN EXCLUDED.disk_total_gb = 0 THEN device_status.disk_used_gb  ELSE EXCLUDED.disk_used_gb  END,
			disk_total_gb = CASE WHEN EXCLUDED.disk_total_gb = 0 THEN device_status.disk_total_gb  ELSE EXCLUDED.disk_total_gb END
		RETURNING (SELECT ack FROM prev) IS DISTINCT FROM ack`,
		tenant, c.Tag, c.Revision, string(c.Phase), c.Error, now, string(c.SB), string(c.TPM2), c.Ack,
		u.CPUPct, u.MemUsedMB, u.MemTotalMB, u.DiskUsedGB, u.DiskTotalGB,
	).Scan(&ackChanged)
	return ackChanged, err
}

// Get implements ports.StatusStore.
func (s *Store) Get(ctx context.Context, tenant, tag string) (observed.DeviceStatus, bool, error) {
	var st observed.DeviceStatus
	var phase, sb, tpm2 string
	err := s.pool.QueryRow(ctx, `
		SELECT tag, revision, phase, error, last_seen, sb_state, tpm2_state, ack,
			cpu_pct, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb
		FROM device_status WHERE tenant = $1 AND tag = $2`, tenant, tag).
		Scan(&st.Tag, &st.Revision, &phase, &st.Error, &st.LastSeen, &sb, &tpm2, &st.Ack,
			&st.Usage.CPUPct, &st.Usage.MemUsedMB, &st.Usage.MemTotalMB, &st.Usage.DiskUsedGB, &st.Usage.DiskTotalGB)
	if errors.Is(err, pgx.ErrNoRows) {
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
		SELECT tag, revision, phase, error, last_seen, sb_state, tpm2_state, ack,
			cpu_pct, mem_used_mb, mem_total_mb, disk_used_gb, disk_total_gb
		FROM device_status WHERE tenant = $1 ORDER BY tag`, tenant)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []observed.DeviceStatus
	for rows.Next() {
		var st observed.DeviceStatus
		var phase, sb, tpm2 string
		if err := rows.Scan(&st.Tag, &st.Revision, &phase, &st.Error, &st.LastSeen, &sb, &tpm2, &st.Ack,
			&st.Usage.CPUPct, &st.Usage.MemUsedMB, &st.Usage.MemTotalMB, &st.Usage.DiskUsedGB, &st.Usage.DiskTotalGB); err != nil {
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
	if errors.Is(err, pgx.ErrNoRows) {
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
