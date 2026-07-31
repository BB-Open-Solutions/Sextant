package app

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
)

// memElevation is an in-memory store. Pending returns everything, on purpose:
// the service must not rely on a store knowing what has expired, because
// expiry is a function of the clock and no row changes when it passes.
type memElevation struct{ m map[string]elevation.Request }

func newMemElevation() *memElevation {
	return &memElevation{m: map[string]elevation.Request{}}
}

func (s *memElevation) Put(_ context.Context, _ string, r elevation.Request) error {
	s.m[r.ID] = r
	return nil
}

func (s *memElevation) Get(_ context.Context, _, id string) (elevation.Request, bool, error) {
	r, ok := s.m[id]
	return r, ok, nil
}

func (s *memElevation) Pending(_ context.Context, _ string) ([]elevation.Request, error) {
	out := make([]elevation.Request, 0, len(s.m))
	for _, r := range s.m {
		out = append(out, r)
	}
	return out, nil
}

func newElevationStack() (*ElevationService, *fakeClock) {
	clock := newFakeClock(testT0)
	return NewElevationService(newMemElevation(), clock, DefaultTenant), clock
}

func TestRaisePollApprove(t *testing.T) {
	ctx := context.Background()
	svc, clock := newElevationStack()

	r, err := svc.Raise(ctx, "lt-1", "bbuijs", "org.freedesktop.NetworkManager.settings.modify.system", "joining the office wifi")
	if err != nil {
		t.Fatal(err)
	}
	if r.State != elevation.Pending {
		t.Fatalf("a new request is %q, want pending", r.State)
	}

	got, ok, err := svc.Poll(ctx, "lt-1", r.ID)
	if err != nil || !ok {
		t.Fatalf("poll: ok=%v err=%v", ok, err)
	}
	if got.Grants(clock.Now()) {
		t.Fatal("a request nobody has answered granted the elevation")
	}

	clock.Advance(10 * time.Second)
	if _, err := svc.Decide(ctx, r.ID, true, "beheerder"); err != nil {
		t.Fatal(err)
	}
	got, _, _ = svc.Poll(ctx, "lt-1", r.ID)
	if !got.Grants(clock.Now()) {
		t.Fatal("an approved request does not grant inside its window")
	}
	if got.DecidedBy != "beheerder" {
		t.Errorf("DecidedBy = %q, want the approver's name", got.DecidedBy)
	}
}

// A device may only read its own requests. The id travels through a user's
// session, so without this check it would be enough for any device to poll
// another's queue and consume an approval meant for somebody else.
func TestADeviceCannotPollAnothersRequest(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	r, err := svc.Raise(ctx, "lt-1", "bbuijs", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := svc.Poll(ctx, "lt-2", r.ID); ok {
		t.Fatal("a different device read the request; the id alone must not be enough")
	}
}

// The queue must reflect the clock, not the store. Nothing writes to a row
// when it expires, so a service that trusted the store's notion of pending
// would show operators requests whose users gave up long ago.
func TestTheQueueDropsWhatHasExpired(t *testing.T) {
	ctx := context.Background()
	svc, clock := newElevationStack()
	if _, err := svc.Raise(ctx, "lt-1", "bbuijs", "", ""); err != nil {
		t.Fatal(err)
	}
	if q, _ := svc.Pending(ctx); len(q) != 1 {
		t.Fatalf("queue has %d entries, want 1", len(q))
	}
	clock.Advance(elevation.TTL)
	q, err := svc.Pending(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(q) != 0 {
		t.Fatalf("queue still holds %d expired request(s)", len(q))
	}
}

func TestAnExpiredRequestCannotBeApproved(t *testing.T) {
	ctx := context.Background()
	svc, clock := newElevationStack()
	r, err := svc.Raise(ctx, "lt-1", "bbuijs", "", "")
	if err != nil {
		t.Fatal(err)
	}
	clock.Advance(elevation.TTL + time.Second)
	if _, err := svc.Decide(ctx, r.ID, true, "beheerder"); err == nil {
		t.Fatal("an expired request was approved; the user gave up minutes ago")
	}
}

// Two ids must never collide, and must not be guessable: the id is the only
// thing tying a waiting device to its answer.
func TestIdsAreDistinct(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		r, err := svc.Raise(ctx, "lt-1", "bbuijs", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if seen[r.ID] {
			t.Fatalf("duplicate elevation id %q", r.ID)
		}
		if len(r.ID) < 32 {
			t.Fatalf("id %q is %d chars; too short to be unguessable", r.ID, len(r.ID))
		}
		seen[r.ID] = true
	}
}

// A device supplies both free-text fields and an operator reads them. One that
// sends a megabyte of text would make the queue unreadable for everybody else.
func TestFreeTextIsBounded(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	long := make([]byte, 5000)
	for i := range long {
		long[i] = 'x'
	}
	r, err := svc.Raise(ctx, "lt-1", "bbuijs", string(long), string(long))
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Action) > 200 || len(r.Reason) > 500 {
		t.Fatalf("action %d chars, reason %d chars; both must be bounded", len(r.Action), len(r.Reason))
	}
}

func TestRaiseRefusesAnAnonymousAsk(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	if _, err := svc.Raise(ctx, "lt-1", "   ", "", ""); err == nil {
		t.Fatal("a request with no user was accepted; nobody could tell who to approve")
	}
	if _, err := svc.Raise(ctx, "", "bbuijs", "", ""); err == nil {
		t.Fatal("a request with no device was accepted")
	}
}
