package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

func TestNotifyStore(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)

	// A direct notification to sub-1 and a broadcast to the "owner" role.
	direct := notify.Notification{ID: "d1", Tenant: "t", Recipient: "sub-1",
		Kind: notify.ChangeMerged, Title: "Your change merged", CreatedAt: now}
	bcast := notify.Notification{ID: "b1", Tenant: "t", Audience: "owner",
		Kind: notify.ApprovalNeeded, Title: "Review needed", CreatedAt: now.Add(time.Second)}
	for _, n := range []notify.Notification{direct, bcast} {
		if err := s.Add(ctx, n); err != nil {
			t.Fatalf("add %s: %v", n.ID, err)
		}
	}

	// sub-1 is an owner: sees both (broadcast newest first), both unread.
	got, err := s.ListFor(ctx, "t", "sub-1", []string{"owner"}, 50)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].ID != "b1" || got[1].ID != "d1" {
		t.Fatalf("list = %+v, want [b1 d1]", got)
	}
	if n, err := s.UnreadCount(ctx, "t", "sub-1", []string{"owner"}); err != nil || n != 2 {
		t.Fatalf("unread = %d (%v), want 2", n, err)
	}

	// sub-2, also an owner, sees only the broadcast (not sub-1's direct one).
	got2, _ := s.ListFor(ctx, "t", "sub-2", []string{"owner"}, 50)
	if len(got2) != 1 || got2[0].ID != "b1" {
		t.Fatalf("sub-2 list = %+v, want [b1]", got2)
	}

	// sub-1 reads the broadcast: unread drops to 1 for sub-1, stays 1 for sub-2.
	if err := s.MarkRead(ctx, "t", "b1", "sub-1"); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(ctx, "t", "sub-1", []string{"owner"}); n != 1 {
		t.Fatalf("sub-1 unread after read = %d, want 1", n)
	}
	if n, _ := s.UnreadCount(ctx, "t", "sub-2", []string{"owner"}); n != 1 {
		t.Fatalf("sub-2 unread = %d, want 1 (read is per-reader)", n)
	}

	// MarkAllRead clears the rest for sub-1.
	if err := s.MarkAllRead(ctx, "t", "sub-1", []string{"owner"}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.UnreadCount(ctx, "t", "sub-1", []string{"owner"}); n != 0 {
		t.Fatalf("sub-1 unread after mark-all = %d, want 0", n)
	}
}
