package postgres

import (
	"context"
	"log/slog"
	"time"
)

// leader.go: single-leader election over the shared database, so the app can
// run multiple replicas (HA) while the background workers that COMMIT (the
// rollout ticker, the upstream watcher) run on exactly one of them. It uses a
// Postgres session-level advisory lock: the holder is the leader, and if it
// dies its session ends and the lock releases, so a standby takes over. No
// extra dependency, no external coordinator - the database the app already
// needs is the coordinator.

// LeaderLoop runs `run` on whichever replica currently holds the advisory
// lock keyed by `key`, restarting the election if leadership is lost. It
// blocks until ctx is cancelled. Stateless workers (reads) do NOT belong
// here - they run on every replica; only committing workers are gated.
func (s *Store) LeaderLoop(ctx context.Context, key int64, log *slog.Logger, run func(ctx context.Context)) {
	for ctx.Err() == nil {
		if err := s.leadOnce(ctx, key, log, run); err != nil && ctx.Err() == nil {
			log.Warn("leader election cycle ended; retrying", "err", err)
			select {
			case <-ctx.Done():
			case <-time.After(5 * time.Second):
			}
		}
	}
}

// leadOnce acquires a dedicated connection, blocks until it wins the advisory
// lock, then runs `run` under a leader-scoped context until the connection
// dies or ctx is cancelled - at which point the lock releases (session end)
// and a standby can take over.
func (s *Store) leadOnce(ctx context.Context, key int64, log *slog.Logger, run func(context.Context)) error {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return err
	}
	defer conn.Release() // releasing the session drops the advisory lock

	// Block until we are the leader. pg_advisory_lock waits; the context
	// bounds the wait so a shutdown does not hang here.
	if _, err := conn.Exec(ctx, "SELECT pg_advisory_lock($1)", key); err != nil {
		return err
	}
	log.Info("became rollout/upstream leader")

	leaderCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); run(leaderCtx) }()

	// Hold leadership: keep the session alive with periodic pings. A failed
	// ping means the connection (and thus the lock) is gone - stop the
	// workers and re-elect.
	ticker := time.NewTicker(5 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			cancel()
			<-done
			return nil
		case <-ticker.C:
			if err := conn.Ping(ctx); err != nil {
				log.Warn("lost leader connection; standing down", "err", err)
				cancel()
				<-done
				return err
			}
		}
	}
}
