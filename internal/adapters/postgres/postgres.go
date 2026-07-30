package postgres

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"strings"
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
	if err := migrateWithRetry(ctx, pool); err != nil {
		pool.Close()
		return nil, fmt.Errorf("postgres migrate: %w", err)
	}
	return &Store{pool: pool}, nil
}

// startupGrace is how long Open keeps trying to reach the database before
// giving up. Sized for "the rest of the cell is still coming up", not for a
// database that is genuinely gone: past this, exiting is the right answer and
// the orchestrator can decide.
const startupGrace = 2 * time.Minute

// migrateWithRetry runs the migrations, retrying while the database is merely
// not there YET.
//
// A control plane that dies because its database had not finished starting is
// fragile for no good reason. Observed on a fresh cell (2026-07-30): the pod
// came up before Cilium had programmed the namespace policy, every connection
// was refused with "operation not permitted", the process exited, and Helm
// gave up and rolled the release back - so what was a few seconds of ordinary
// startup ordering looked exactly like a broken deployment. It recovered by
// itself, which is the tell: nothing was wrong, we were simply early.
//
// Only CONNECT failures are retried. A migration that runs and fails is a real
// error - a bad schema change does not get better by being run again - so that
// returns immediately.
func migrateWithRetry(ctx context.Context, pool *pgxpool.Pool) error {
	deadline := time.Now().Add(startupGrace)
	for attempt := 1; ; attempt++ {
		err := Migrate(ctx, pool)
		if err == nil {
			return nil
		}
		if !isUnreachable(err) || time.Now().After(deadline) || ctx.Err() != nil {
			return err
		}
		slog.Warn("database not reachable yet, retrying",
			"attempt", attempt, "err", err)
		select {
		case <-time.After(2 * time.Second):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// isUnreachable reports whether an error is "the database is not there yet"
// rather than "the database said no". Matched on the connect path pgx reports
// before any statement runs.
func isUnreachable(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	s := err.Error()
	for _, frag := range []string{
		"connection refused",
		"operation not permitted", // a network policy not yet programmed
		"no route to host",
		"i/o timeout",
		"connect: connection reset by peer",
		"server is not accepting connections",
		"the database system is starting up",
	} {
		if strings.Contains(s, frag) {
			return true
		}
	}
	return false
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

// RingStragglers lists the devices holding a ring under 100%: never seen,
// off-target, offline on target, or erroring - with a short reason each.
// Capped: the list informs an operator, it is not an inventory export.
func (c *Convergence) RingStragglers(ctx context.Context, groups []string, target string) ([]rollout.Straggler, error) {
	tags := c.groupTags(groups)
	if len(tags) == 0 {
		return nil, nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	cutoff := now().Add(-observed.OnlineWindow)
	absentCutoff := now().Add(-observed.AbsentWindow)
	// Absent devices (silent beyond the absent window, or never seen) do not
	// hold the wave - say so, instead of a reason that reads like a blocker.
	rows, err := c.store.pool.Query(ctx, `
		SELECT t.tag,
			CASE
				WHEN d.tag IS NULL THEN 'not seen yet; joins on first check-in'
				WHEN d.last_seen < $5 THEN 'away; catches up on next check-in'
				WHEN d.revision <> $3 THEN 'not on target yet'
				WHEN d.last_seen < $4 THEN 'offline'
				WHEN d.error <> '' THEN 'error: ' || left(d.error, 120)
				ELSE 'phase ' || d.phase
			END
		FROM unnest($2::text[]) AS t(tag)
		LEFT JOIN device_status d ON d.tenant = $1 AND d.tag = t.tag
		WHERE d.tag IS NULL
			OR d.revision <> $3
			OR d.last_seen < $4
			OR d.error <> ''
			OR (d.phase <> '' AND d.phase <> 'running')
		ORDER BY t.tag
		LIMIT 20`,
		c.tenant, tags, target, cutoff, absentCutoff)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []rollout.Straggler
	for rows.Next() {
		var st rollout.Straggler
		if err := rows.Scan(&st.Tag, &st.Reason); err != nil {
			return nil, err
		}
		out = append(out, st)
	}
	return out, rows.Err()
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

// groupTags flattens a wave's groups to one tag list (groups are disjoint by
// plan validation, so no dedup is needed).
func (c *Convergence) groupTags(groups []string) []string {
	tags := make([]string, 0, len(groups))
	for _, g := range groups {
		tags = append(tags, c.Tags(g)...)
	}
	return tags
}

// NewConvergence wires the convergence source.
func (s *Store) NewConvergence(tenant string, tags func(group string) []string) *Convergence {
	return &Convergence{store: s, tenant: tenant, Tags: tags}
}

// RingStatus implements ports.ConvergenceSource with one aggregate query.
func (c *Convergence) RingStatus(ctx context.Context, groups []string, target string) (rollout.RingStatus, error) {
	tags := c.groupTags(groups)
	if len(tags) == 0 {
		return rollout.RingStatus{}, nil
	}
	now := time.Now
	if c.Now != nil {
		now = c.Now
	}
	cutoff := now().Add(-observed.OnlineWindow)
	absentCutoff := now().Add(-observed.AbsentWindow)

	var rs rollout.RingStatus
	var present int
	// Total counts the ring's devices (config is truth for membership);
	// on-target and healthy come from observed rows, counted over the
	// PRESENT population only (silent beyond the absent window = a shut
	// laptop, not a blocker - it catches up on its next check-in). A device
	// with no observed row at all is absent by the same rule.
	rs.Total = len(tags)
	// Broken is its own count (recently seen on target WITH an error or a
	// stuck phase), never OnTarget-Healthy: those two are filtered over
	// different recency windows, and their difference would count an
	// on-target device that dozed past the online window as a bad release.
	err := c.store.pool.QueryRow(ctx, `
		SELECT
			COUNT(*) FILTER (WHERE last_seen >= $5),
			COUNT(*) FILTER (WHERE revision = $3 AND last_seen >= $5),
			COUNT(*) FILTER (WHERE revision = $3
				AND last_seen >= $4
				AND (phase = '' OR phase = 'running')
				AND error = ''),
			COUNT(*) FILTER (WHERE revision = $3
				AND last_seen >= $4
				AND (error <> '' OR (phase <> '' AND phase <> 'running')))
		FROM device_status
		WHERE tenant = $1 AND tag = ANY($2)`,
		c.tenant, tags, target, cutoff, absentCutoff).Scan(&present, &rs.OnTarget, &rs.Healthy, &rs.Broken)
	if err != nil {
		return rollout.RingStatus{}, err
	}
	rs.Absent = rs.Total - present
	return rs, nil
}
