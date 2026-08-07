package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// retention_test.go covers the DELETE statements behind storage limitation.
//
// These run against real Postgres rather than a fake, deliberately. A fake
// would assert that the Go code calls the method; what actually matters here
// is what the SQL removes and - much more - what it leaves alone. A retention
// sweep that takes one row too many is silent data loss, and the only way to
// know is to run the statement.

func TestRetentionRemovesOldAndKeepsRecent(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)
	cutoff := now.Add(-200 * 24 * time.Hour)

	// Two notifications, one either side of the cutoff.
	for _, n := range []notify.Notification{
		{ID: "old", Tenant: "t1", Recipient: "ada", Kind: notify.ApprovalNeeded, Title: "old", CreatedAt: old},
		{ID: "new", Tenant: "t1", Recipient: "ada", Kind: notify.ApprovalNeeded, Title: "new", CreatedAt: now},
		// Another tenant's old row must survive: the sweep is per tenant, and
		// a cell that deletes a neighbour's data is worse than one that keeps
		// its own too long.
		{ID: "other", Tenant: "t2", Recipient: "ada", Kind: notify.ApprovalNeeded, Title: "other", CreatedAt: old},
	} {
		if err := s.Add(ctx, n); err != nil {
			t.Fatalf("seed notification: %v", err)
		}
	}

	got, err := s.DeleteNotificationsBefore(ctx, "t1", cutoff)
	if err != nil {
		t.Fatalf("delete notifications: %v", err)
	}
	if got != 1 {
		t.Errorf("removed %d notifications, want 1", got)
	}
	left, err := s.ListFor(ctx, "t1", "ada", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(left) != 1 || left[0].ID != "new" {
		t.Errorf("survivors = %+v, want only the recent one", left)
	}
	other, err := s.ListFor(ctx, "t2", "ada", nil, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(other) != 1 {
		t.Errorf("the other tenant lost %d rows", 1-len(other))
	}
}

func TestRetentionRemovesOldElevationRequests(t *testing.T) {
	base := openStore(t)
	s := base.Elevation()
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)

	for _, r := range []elevation.Request{
		{ID: "e-old", Tag: "lt-1", User: "ada", Action: "a", Reason: "r", State: elevation.Pending, Created: old},
		{ID: "e-new", Tag: "lt-1", User: "ada", Action: "a", Reason: "r", State: elevation.Pending, Created: now},
	} {
		if err := s.Put(ctx, "t1", r); err != nil {
			t.Fatalf("seed elevation: %v", err)
		}
	}

	got, err := base.DeleteElevationBefore(ctx, "t1", now.Add(-200*24*time.Hour))
	if err != nil {
		t.Fatalf("delete elevation: %v", err)
	}
	if got != 1 {
		t.Errorf("removed %d elevation requests, want 1", got)
	}
	if _, ok, _ := s.Get(ctx, "t1", "e-new"); !ok {
		t.Error("the recent request was removed")
	}
	if _, ok, _ := s.Get(ctx, "t1", "e-old"); ok {
		t.Error("the old request survived")
	}
}

// TestRetentionNeverForgetsALiveDevice is the important one. A device that
// exists must keep its check-in history however long it has been silent -
// that silence IS the finding, and deleting it would erase the evidence that
// something is wrong while making the fleet look healthy.
func TestRetentionNeverForgetsALiveDevice(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	old := now.Add(-400 * 24 * time.Hour)

	for tag, seen := range map[string]time.Time{
		"live-and-silent": old, // in the fleet, quiet for over a year
		"gone-and-silent": old, // not in the fleet any more
		"live-and-noisy":  now,
	} {
		if _, err := s.Upsert(ctx, "t1", observed.CheckIn{Tag: tag, Revision: "r", Phase: observed.Running}, seen); err != nil {
			t.Fatalf("seed status for %s: %v", tag, err)
		}
	}

	known := map[string]bool{"live-and-silent": true, "live-and-noisy": true}
	got, err := s.DeleteDeviceStatusBefore(ctx, "t1", now.Add(-200*24*time.Hour), known)
	if err != nil {
		t.Fatalf("delete device status: %v", err)
	}
	if got != 1 {
		t.Errorf("removed %d rows, want 1 (only the forgotten tag)", got)
	}
	if _, ok, _ := s.Get(ctx, "t1", "live-and-silent"); !ok {
		t.Error("a device still in the fleet lost its history because it was quiet")
	}
	if _, ok, _ := s.Get(ctx, "t1", "gone-and-silent"); ok {
		t.Error("a tag the fleet has forgotten kept its check-ins")
	}
}

// TestRetentionRefusesAnEmptyFleet: with no known tags every row looks
// forgotten, so a fleet document that failed to load would take the whole
// observed plane with it. The store refuses even though the caller does too.
func TestRetentionRefusesAnEmptyFleet(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC()
	if _, err := s.Upsert(ctx, "t1",
		observed.CheckIn{Tag: "lt-1", Revision: "r", Phase: observed.Running},
		now.Add(-400*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, err := s.DeleteDeviceStatusBefore(ctx, "t1", now, map[string]bool{})
	if err != nil {
		t.Fatalf("empty fleet: %v", err)
	}
	if got != 0 {
		t.Errorf("removed %d rows against an empty fleet document", got)
	}
	if _, ok, _ := s.Get(ctx, "t1", "lt-1"); !ok {
		t.Error("the observed plane was emptied by a fleet document that says nothing")
	}
}
