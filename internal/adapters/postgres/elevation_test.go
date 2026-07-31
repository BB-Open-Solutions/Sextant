package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
)

// The store holds facts, never a computed state: whether a request is still
// pending depends on the clock, and a row does not change when the window
// passes. These tests pin that, because the tempting shortcut - a state column
// kept up to date by whatever process happens to be running - is exactly the
// thing that lies after a restart.

func TestElevationRoundTripKeepsTheDecision(t *testing.T) {
	s := openStore(t).Elevation()
	ctx := context.Background()

	r := elevation.Request{
		ID: "e1", Tag: "lt-1", User: "bbuijs",
		Action: "org.freedesktop.NetworkManager.settings.modify.system",
		Reason: "joining the office wifi", State: elevation.Pending, Created: t0,
	}
	if err := s.Put(ctx, "t1", r); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.Get(ctx, "t1", "e1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.State != elevation.Pending || !got.Decided.IsZero() {
		t.Fatalf("stored request came back as %q decided=%v, want pending and undecided", got.State, got.Decided)
	}
	if got.Reason != r.Reason || got.Action != r.Action {
		t.Errorf("free text did not survive: action=%q reason=%q", got.Action, got.Reason)
	}

	decided, err := got.Decide(true, "beheerder", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "t1", decided); err != nil {
		t.Fatal(err)
	}
	got, _, err = s.Get(ctx, "t1", "e1")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != elevation.Approved {
		t.Errorf("state after approval is %q, want approved", got.State)
	}
	if got.DecidedBy != "beheerder" {
		t.Errorf("DecidedBy = %q; the audit trail must name the approver", got.DecidedBy)
	}
}

// A denial must round-trip as a denial and not as "not approved yet". The
// column is a nullable boolean, so the null-versus-false distinction is
// exactly where this would go wrong.
func TestElevationDenialIsNotMistakenForPending(t *testing.T) {
	s := openStore(t).Elevation()
	ctx := context.Background()
	r := elevation.Request{ID: "e2", Tag: "lt-1", User: "bbuijs", State: elevation.Pending, Created: t0}
	if err := s.Put(ctx, "t1", r); err != nil {
		t.Fatal(err)
	}
	denied, err := r.Decide(false, "beheerder", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "t1", denied); err != nil {
		t.Fatal(err)
	}
	got, _, err := s.Get(ctx, "t1", "e2")
	if err != nil {
		t.Fatal(err)
	}
	if got.State != elevation.Denied {
		t.Fatalf("a denial came back as %q", got.State)
	}
	if q, err := s.Pending(ctx, "t1"); err != nil || len(q) != 0 {
		t.Fatalf("a denied request is still in the queue: %+v %v", q, err)
	}
}

// Pending returns UNDECIDED requests, oldest first, and does not try to apply
// expiry - the row does not change when the window passes, so a store
// filtering on time would be guessing at a clock it does not own.
func TestElevationPendingIsOldestFirstAndTenantScoped(t *testing.T) {
	s := openStore(t).Elevation()
	ctx := context.Background()
	for _, r := range []elevation.Request{
		{ID: "b", Tag: "lt-1", User: "u", State: elevation.Pending, Created: t0.Add(2 * time.Minute)},
		{ID: "a", Tag: "lt-1", User: "u", State: elevation.Pending, Created: t0},
		{ID: "c", Tag: "lt-1", User: "u", State: elevation.Pending, Created: t0.Add(time.Minute)},
	} {
		if err := s.Put(ctx, "t1", r); err != nil {
			t.Fatal(err)
		}
	}
	// Another tenant's request must not appear in this queue.
	if err := s.Put(ctx, "t2", elevation.Request{
		ID: "other", Tag: "lt-9", User: "u", State: elevation.Pending, Created: t0,
	}); err != nil {
		t.Fatal(err)
	}

	q, err := s.Pending(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}
	ids := make([]string, 0, len(q))
	for _, r := range q {
		ids = append(ids, r.ID)
	}
	if len(ids) != 3 || ids[0] != "a" || ids[1] != "c" || ids[2] != "b" {
		t.Fatalf("queue is %v, want [a c b] - oldest first, this tenant only", ids)
	}
	// Even long past the window, the store still returns them: applying expiry
	// is the caller's job, and this test would break if the store started
	// second-guessing that.
	if len(q) != 3 {
		t.Fatal("the store dropped rows on its own")
	}
}

func TestElevationPruneRemovesOldRequests(t *testing.T) {
	s := openStore(t).Elevation()
	ctx := context.Background()
	if err := s.Put(ctx, "t1", elevation.Request{
		ID: "old", Tag: "lt-1", User: "u", State: elevation.Pending, Created: t0,
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "t1", elevation.Request{
		ID: "new", Tag: "lt-1", User: "u", State: elevation.Pending, Created: t0.Add(time.Hour),
	}); err != nil {
		t.Fatal(err)
	}
	n, err := s.Prune(ctx, t0.Add(30*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("pruned %d rows, want 1", n)
	}
	if _, ok, _ := s.Get(ctx, "t1", "new"); !ok {
		t.Error("prune removed a request that was inside the window")
	}
	if _, ok, _ := s.Get(ctx, "t1", "old"); ok {
		t.Error("prune left the old request behind")
	}
}

func TestElevationGetMissingIsNotAnError(t *testing.T) {
	s := openStore(t).Elevation()
	_, ok, err := s.Get(context.Background(), "t1", "nope")
	if err != nil {
		t.Fatalf("a missing request returned an error: %v", err)
	}
	if ok {
		t.Fatal("a missing request reported as found")
	}
}
