package app

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// memStatus is an in-memory ports.StatusStore for check-in tests.
type memStatus struct {
	m map[string]observed.DeviceStatus
}

func newMemStatus() *memStatus { return &memStatus{m: map[string]observed.DeviceStatus{}} }

// Upsert mirrors the real store's contract: ackChanged reports whether the
// ack differs from what was stored immediately before this write, computed
// as part of the same call (no separate prior read by the caller).
func (s *memStatus) Upsert(_ context.Context, _ string, c observed.CheckIn, _ time.Time) (bool, error) {
	prev, existed := s.m[c.Tag]
	ackChanged := !existed || prev.Ack != c.Ack
	s.m[c.Tag] = observed.DeviceStatus{Tag: c.Tag, Ack: c.Ack}
	return ackChanged, nil
}
func (s *memStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	st, ok := s.m[tag]
	return st, ok, nil
}
func (s *memStatus) List(context.Context, string) ([]observed.DeviceStatus, error) { return nil, nil }
func (s *memStatus) Ping(context.Context) error                                    { return nil }

// nopFacts is an inert ports.InventoryStore.
type nopFacts struct{}

func (nopFacts) PutFacts(context.Context, string, string, []byte, time.Time) error { return nil }
func (nopFacts) GetFacts(context.Context, string, string) ([]byte, time.Time, bool, error) {
	return nil, time.Time{}, false, nil
}

func TestCheckInNotifiesWipeOncePerTransition(t *testing.T) {
	store := newMemStatus()
	notifyStore := newFakeNotifyStore()
	notifier := NewNotifyService(notifyStore, clockAt{time.Unix(1, 0)}, "default")
	inv := NewInventoryService(store, nopFacts{}, clockAt{time.Unix(1, 0)}, "default").
		WithNotifier(notifier, []string{"owners"})
	ctx := context.Background()

	beat := func(ack string) {
		if err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-01", Ack: ack}, nil); err != nil {
			t.Fatalf("checkin(%q): %v", ack, err)
		}
	}

	// A plain beat never notifies.
	beat("")
	// The first wipe ack notifies once...
	beat(observed.AckWipe)
	// ...and repeated echoes of the same ack do not re-notify.
	beat(observed.AckWipe)
	beat(observed.AckWipe)

	got := 0
	for _, n := range notifyStore.added {
		if n.Kind == notify.WipeExecuted {
			got++
		}
	}
	if got != 1 {
		t.Fatalf("want exactly 1 wipe notification across repeated echoes, got %d", got)
	}
}

// TestCheckInRejectsBadFactsBeforeAnySideEffect (finding: CheckIn recorded
// status and fired the wipe notification before validating facts): a
// malformed facts payload must reject the whole check-in with no status
// write and no notification, not a partially-applied call.
func TestCheckInRejectsBadFactsBeforeAnySideEffect(t *testing.T) {
	store := newMemStatus()
	notifyStore := newFakeNotifyStore()
	notifier := NewNotifyService(notifyStore, clockAt{time.Unix(1, 0)}, "default")
	inv := NewInventoryService(store, nopFacts{}, clockAt{time.Unix(1, 0)}, "default").
		WithNotifier(notifier, []string{"owners"})
	ctx := context.Background()

	// Invalid JSON alongside a wipe ack: the facts error must win, and it must
	// win BEFORE the status upsert and the wipe notification happen.
	err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-01", Ack: observed.AckWipe}, []byte("{not json"))
	if err == nil {
		t.Fatal("want an error for invalid facts JSON")
	}
	if _, ok, _ := store.Get(ctx, "default", "nuc-01"); ok {
		t.Fatal("status must not be recorded when facts validation fails")
	}
	for _, n := range notifyStore.added {
		if n.Kind == notify.WipeExecuted {
			t.Fatal("wipe notification must not fire when facts validation fails")
		}
	}

	// Oversized facts: same guarantee.
	big := make([]byte, maxFactsBytes+1)
	for i := range big {
		big[i] = 'x'
	}
	if err := inv.CheckIn(ctx, observed.CheckIn{Tag: "nuc-02", Ack: observed.AckWipe}, big); err == nil {
		t.Fatal("want an error for an oversized facts payload")
	}
	if _, ok, _ := store.Get(ctx, "default", "nuc-02"); ok {
		t.Fatal("status must not be recorded for an oversized facts payload")
	}
}

func TestCheckInNoNotifierIsInert(t *testing.T) {
	// Without a notifier (no Postgres), a wipe ack still records fine.
	inv := NewInventoryService(newMemStatus(), nopFacts{}, clockAt{time.Unix(1, 0)}, "default")
	if err := inv.CheckIn(context.Background(), observed.CheckIn{Tag: "x", Ack: observed.AckWipe}, nil); err != nil {
		t.Fatalf("checkin: %v", err)
	}
}
