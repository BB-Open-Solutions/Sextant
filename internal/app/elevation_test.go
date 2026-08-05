package app

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
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

// capturingNotifier records what an emitter sent, and can fail on demand.
type capturingNotifier struct {
	sent []notify.Notification
	err  error
}

func (c *capturingNotifier) Emit(_ context.Context, n notify.Notification) error {
	c.sent = append(c.sent, n)
	return c.err
}

// TestRaiseTellsTheApprovers: a request expires in five minutes, so a queue
// nobody is told about is a queue nobody answers. Before this the only way an
// operator learned of a request was having /elevation open at the moment it
// arrived.
func TestRaiseTellsTheApprovers(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	n := &capturingNotifier{}
	svc.WithNotifier(n, []string{"platform-owners", "second-line"})

	if _, err := svc.Raise(ctx, "lt-1", "bbuijs", "install printer driver", "new floor printer"); err != nil {
		t.Fatal(err)
	}
	if len(n.sent) != 2 {
		t.Fatalf("every approver audience should hear about it, got %d", len(n.sent))
	}
	got := n.sent[0]
	if got.Kind != notify.ElevationRequested {
		t.Errorf("kind = %q", got.Kind)
	}
	if got.Audience != "platform-owners" {
		t.Errorf("audience = %q", got.Audience)
	}
	if got.Link != "/elevation" {
		t.Errorf("link does not point at the queue: %q", got.Link)
	}
	// An operator decides on who, where and what. A message that omits them
	// costs a page load before it means anything.
	for _, want := range []string{"bbuijs", "lt-1"} {
		if !strings.Contains(got.Title, want) {
			t.Errorf("title %q does not carry %q", got.Title, want)
		}
	}
	for _, want := range []string{"install printer driver", "new floor printer"} {
		if !strings.Contains(got.Body, want) {
			t.Errorf("body %q does not carry %q", got.Body, want)
		}
	}
}

// TestRaiseSurvivesABrokenNotifier: somebody is standing at a machine with a
// dialog open on a five-minute clock. A notification store or SMTP server
// having a bad day must not turn that into a failed request.
func TestRaiseSurvivesABrokenNotifier(t *testing.T) {
	ctx := context.Background()
	svc, _ := newElevationStack()
	svc.WithNotifier(&capturingNotifier{err: errors.New("store down")}, []string{"owners"})

	r, err := svc.Raise(ctx, "lt-1", "bbuijs", "mount usb", "")
	if err != nil {
		t.Fatalf("a broken notifier failed the request: %v", err)
	}
	// And the request is genuinely queued, not merely returned.
	pending, err := svc.Pending(ctx)
	if err != nil || len(pending) != 1 || pending[0].ID != r.ID {
		t.Fatalf("request not queued: %v %v", pending, err)
	}
}

// TestRaiseWithoutANotifierStillWorks: notifications are optional wiring (no
// Postgres, no notifications), and the queue must not depend on them.
func TestRaiseWithoutANotifierStillWorks(t *testing.T) {
	svc, _ := newElevationStack()
	if _, err := svc.Raise(context.Background(), "lt-1", "bbuijs", "mount usb", ""); err != nil {
		t.Fatalf("unwired notifier broke the request: %v", err)
	}
}

// TestAMissingReasonReadsAsASentence: a device may send no reason, and an
// empty pair of brackets reads like something went missing.
func TestAMissingReasonReadsAsASentence(t *testing.T) {
	svc, _ := newElevationStack()
	n := &capturingNotifier{}
	svc.WithNotifier(n, []string{"owners"})
	if _, err := svc.Raise(context.Background(), "lt-1", "bbuijs", "mount usb", ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(n.sent[0].Body, "no reason given") {
		t.Fatalf("body = %q", n.sent[0].Body)
	}
}
