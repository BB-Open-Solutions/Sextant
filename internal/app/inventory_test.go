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

func (s *memStatus) Upsert(_ context.Context, _ string, c observed.CheckIn, _ time.Time) error {
	s.m[c.Tag] = observed.DeviceStatus{Tag: c.Tag, Ack: c.Ack}
	return nil
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

func TestCheckInNoNotifierIsInert(t *testing.T) {
	// Without a notifier (no Postgres), a wipe ack still records fine.
	inv := NewInventoryService(newMemStatus(), nopFacts{}, clockAt{time.Unix(1, 0)}, "default")
	if err := inv.CheckIn(context.Background(), observed.CheckIn{Tag: "x", Ack: observed.AckWipe}, nil); err != nil {
		t.Fatalf("checkin: %v", err)
	}
}
