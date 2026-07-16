package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func TestPostureRoundTripAndSticky(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	t1 := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// First posture report.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{
		Tag: "lt-1", Revision: "v1",
		SB: observed.SBAudit, TPM2: observed.TPM2Present}, t1); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ := s.Get(ctx, "default", "lt-1")
	if st.SB != observed.SBAudit || st.TPM2 != observed.TPM2Present {
		t.Fatalf("posture = %+v", st)
	}

	// An old agent (empty posture) must not erase stored posture.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1", Revision: "v1"}, t1.Add(time.Minute)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.SB != observed.SBAudit || st.TPM2 != observed.TPM2Present {
		t.Fatalf("empty posture clobbered stored: %+v", st)
	}

	// Progress updates it.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{
		Tag: "lt-1", SB: observed.SBEnforcing, TPM2: observed.TPM2Enrolled}, t1.Add(2*time.Minute)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.SB != observed.SBEnforcing || st.TPM2 != observed.TPM2Enrolled {
		t.Fatalf("posture progress = %+v", st)
	}

	// List carries posture too.
	all, _ := s.List(ctx, "default")
	if len(all) != 1 || all[0].SB != observed.SBEnforcing {
		t.Fatalf("list posture = %+v", all)
	}
}
