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
// worker concurrently, and when the current leader stops a standby takes over.
func TestLeaderLoopSingleRunner(t *testing.T) {
	s := openStore(t)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	const key int64 = 424242

	var running int32 // how many workers are active right now
	var maxSeen int32
	worker := func(ctx context.Context) {
		n := atomic.AddInt32(&running, 1)
		for {
			if m := atomic.LoadInt32(&maxSeen); n > m {
				atomic.CompareAndSwapInt32(&maxSeen, m, n)
			}
			select {
			case <-ctx.Done():
				atomic.AddInt32(&running, -1)
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	done1 := make(chan struct{})
	go func() { defer close(done1); s.LeaderLoop(ctx1, key, log, worker) }()
	go s.LeaderLoop(ctx2, key, log, worker)

	// Let leadership settle; only one worker should ever be active.
	time.Sleep(400 * time.Millisecond)
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("max concurrent workers = %d, want 1", got)
	}

	// Stop the current leader; the standby must take over (worker runs again).
	cancel1()
	<-done1
	time.Sleep(500 * time.Millisecond)
	if got := atomic.LoadInt32(&running); got != 1 {
		t.Fatalf("after failover, running workers = %d, want 1", got)
	}
	if got := atomic.LoadInt32(&maxSeen); got != 1 {
		t.Fatalf("failover ran two workers at once: max=%d", got)
	}
}
