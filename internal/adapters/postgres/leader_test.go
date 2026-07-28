package postgres

import (
	"context"
	"io"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

// TestLeaderLoopSingleRunner: two LeaderLoops on the same key never run their
// worker concurrently, and when the current leader stops the standby takes
// over. No flat sleeps: every step waits on observed state, and the replica
// that gets cancelled is the one that actually holds leadership.
func TestLeaderLoopSingleRunner(t *testing.T) {
	s := openStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const key int64 = 424242

	var running int32 // how many workers are active right now
	var maxSeen int32
	var leaderID atomic.Int32 // which replica's worker ran most recently
	mkWorker := func(id int32) func(ctx context.Context) {
		return func(ctx context.Context) {
			n := atomic.AddInt32(&running, 1)
			for {
				m := atomic.LoadInt32(&maxSeen)
				if n <= m || atomic.CompareAndSwapInt32(&maxSeen, m, n) {
					break
				}
			}
			leaderID.Store(id)
			<-ctx.Done()
			atomic.AddInt32(&running, -1)
		}
	}

	waitFor := func(desc string, cond func() bool) {
		t.Helper()
		deadline := time.Now().Add(10 * time.Second)
		for !cond() {
			if time.Now().After(deadline) {
				t.Fatalf("timeout waiting for %s (running=%d max=%d leader=%d)",
					desc, atomic.LoadInt32(&running), atomic.LoadInt32(&maxSeen), leaderID.Load())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}

	type replica struct {
		cancel context.CancelFunc
		done   chan struct{}
	}
	replicas := make([]replica, 2)
	for i := range replicas {
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		replicas[i] = replica{cancel: cancel, done: done}
		id := int32(i + 1)
		go func() { defer close(done); s.LeaderLoop(ctx, key, log, mkWorker(id)) }()
	}
	defer func() {
		for _, r := range replicas {
			r.cancel()
			<-r.done
		}
	}()

	// Exactly one replica becomes leader; the other stays blocked.
	waitFor("first leader", func() bool { return atomic.LoadInt32(&running) == 1 })
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("max concurrent workers = %d, want 1", got)
	}

	// Stop the replica that actually leads; the standby must take over.
	first := leaderID.Load()
	replicas[first-1].cancel()
	<-replicas[first-1].done
	waitFor("failover to the standby", func() bool {
		return atomic.LoadInt32(&running) == 1 && leaderID.Load() != first
	})
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("failover ran two workers at once: max=%d", got)
	}
}
