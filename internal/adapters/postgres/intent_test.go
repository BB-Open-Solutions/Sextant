package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func TestAckStickyRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)

	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1", Ack: "lock"}, t1); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ := s.Get(ctx, "default", "lt-1")
	if st.Ack != "lock" {
		t.Fatalf("ack = %q", st.Ack)
	}
	// An ordinary beat without ack must not erase the recorded one.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1", Revision: "v2"}, t1.Add(time.Minute)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.Ack != "lock" {
		t.Fatalf("ack clobbered: %q", st.Ack)
	}
}
