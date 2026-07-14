package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// lockedMemStatus is a ports.StatusStore whose Upsert computes ackChanged
// atomically under its own lock, mirroring the real store's single
// SQL statement (CTE + RETURNING) - the fix for the double-notify bug: the
// read-compare-write is one critical section, not CheckIn doing a separate
// Get before Upsert.
type lockedMemStatus struct {
	mu sync.Mutex
	m  map[string]observed.DeviceStatus
}

func newLockedMemStatus() *lockedMemStatus {
	return &lockedMemStatus{m: map[string]observed.DeviceStatus{}}
}

func (s *lockedMemStatus) Upsert(_ context.Context, _ string, c observed.CheckIn, _ time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.m[c.Tag]
	ackChanged := !existed || prev.Ack != c.Ack
	s.m[c.Tag] = observed.DeviceStatus{Tag: c.Tag, Ack: c.Ack}
	return ackChanged, nil
}
func (s *lockedMemStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, ok := s.m[tag]
	return st, ok, nil
}
func (s *lockedMemStatus) List(context.Context, string) ([]observed.DeviceStatus, error) {
	return nil, nil
}
func (s *lockedMemStatus) Ping(context.Context) error { return nil }

// TestCheckInConcurrentWipeAcksNotifyExactlyOnce is the atomicity proof for
// the fix: before it, CheckIn read the prior status, decided notifyWipe, and
// only THEN upserted - two concurrent check-ins racing the same tag's
// transition into a wipe ack could both observe the same prior (non-wipe)
// ack and both raise the notification. Deriving the transition from the
// store's own atomic write closes that window: of N racers reporting the
// SAME fresh wipe ack, exactly one must see ackChanged=true.
func TestCheckInConcurrentWipeAcksNotifyExactlyOnce(t *testing.T) {
	store := newLockedMemStatus()
	notifyStore := newFakeNotifyStore()
	notifier := NewNotifyService(notifyStore, clockAt{time.Unix(1, 0)}, "default")
	inv := NewInventoryService(store, nopFacts{}, clockAt{time.Unix(1, 0)}, "default").
		WithNotifier(notifier, []string{"owners"})
	ctx := context.Background()

	// Every racer reports the SAME fresh wipe ack for the SAME tag: only
	// the one that atomically wins the ack-changed transition may notify.
	const racers = 16
	var wg sync.WaitGroup
	for i := 0; i < racers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-01", Ack: observed.AckWipe}, nil)
		}()
	}
	wg.Wait()

	got := 0
	for _, n := range notifyStore.added {
		if n.Kind == notify.WipeExecuted {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("wipe notifications = %d, want exactly 1 of %d concurrent racers", got, racers)
	}
}
