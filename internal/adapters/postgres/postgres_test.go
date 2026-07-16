package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

var t0 = time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

func TestMigrateIdempotent(t *testing.T) {
	s := openStore(t)
	// Open already migrated; a second run must be a no-op.
	if err := Migrate(context.Background(), s.pool); err != nil {
		t.Fatal(err)
	}
}

func TestUpsertGetList(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, ok, err := s.Get(ctx, "default", "lt-1"); err != nil || ok {
		t.Fatalf("empty get = %v %v", ok, err)
	}

	// First check-in.
	_, err := s.Upsert(ctx, "default",
		observed.CheckIn{Tag: "lt-1", Revision: "v1", Phase: observed.Running}, t0)
	if err != nil {
		t.Fatal(err)
	}
	st, ok, err := s.Get(ctx, "default", "lt-1")
	if err != nil || !ok || st.Revision != "v1" || st.Phase != observed.Running {
		t.Fatalf("get = %+v %v %v", st, ok, err)
	}

	// A light heartbeat (no revision/phase) must not clobber stored values.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1"}, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.Revision != "v1" || st.Phase != observed.Running {
		t.Fatalf("heartbeat clobbered state: %+v", st)
	}
	if !st.LastSeen.After(t0) {
		t.Fatal("heartbeat did not refresh last_seen")
	}

	// An error report sets and a clean report clears the error.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1", Error: "unit failed"}, t0.Add(2*time.Minute)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.Error != "unit failed" {
		t.Fatalf("error not stored: %+v", st)
	}
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "lt-1"}, t0.Add(3*time.Minute)); err != nil {
		t.Fatalf("setup: %v", err)
	}
	st, _, _ = s.Get(ctx, "default", "lt-1")
	if st.Error != "" {
		t.Fatalf("error not cleared: %+v", st)
	}

	// List sorted by tag.
	if _, err := s.Upsert(ctx, "default", observed.CheckIn{Tag: "aa-1", Revision: "v1"}, t0); err != nil {
		t.Fatalf("setup: %v", err)
	}
	list, err := s.List(ctx, "default")
	if err != nil || len(list) != 2 || list[0].Tag != "aa-1" {
		t.Fatalf("list = %+v, %v", list, err)
	}
}

// TestUpsertUtilisationPartialReadingKept guards the per-dimension guard: a
// beat that reports cpu+disk but whose memory probe failed (mem_total_mb=0)
// must still write the fresh cpu/disk figures, not fall back to the whole
// row's stale values just because one dimension came back empty.
func TestUpsertUtilisationPartialReadingKept(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	full := observed.CheckIn{Tag: "lt-1", Usage: observed.Usage{
		CPUPct: 10, MemUsedMB: 1000, MemTotalMB: 8000, DiskUsedGB: 20, DiskTotalGB: 100,
	}}
	if _, err := s.Upsert(ctx, "default", full, t0); err != nil {
		t.Fatal(err)
	}

	// Partial beat: cpu and disk read fine, memory probe failed (all-zero).
	partial := observed.CheckIn{Tag: "lt-1", Usage: observed.Usage{
		CPUPct: 55, MemUsedMB: 0, MemTotalMB: 0, DiskUsedGB: 30, DiskTotalGB: 100,
	}}
	if _, err := s.Upsert(ctx, "default", partial, t0.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}

	st, ok, err := s.Get(ctx, "default", "lt-1")
	if err != nil || !ok {
		t.Fatalf("get = %v %v", ok, err)
	}
	if st.Usage.CPUPct != 55 || st.Usage.DiskUsedGB != 30 || st.Usage.DiskTotalGB != 100 {
		t.Fatalf("fresh cpu/disk dropped: %+v", st.Usage)
	}
	if st.Usage.MemUsedMB != 1000 || st.Usage.MemTotalMB != 8000 {
		t.Fatalf("stale memory not preserved across failed probe: %+v", st.Usage)
	}
}

func TestTenantIsolation(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	if _, err := s.Upsert(ctx, "org-a", observed.CheckIn{Tag: "lt-1", Revision: "v1"}, t0); err != nil {
		t.Fatalf("setup: %v", err)
	}
	if _, err := s.Upsert(ctx, "org-b", observed.CheckIn{Tag: "lt-1", Revision: "v9"}, t0); err != nil {
		t.Fatalf("setup: %v", err)
	}

	a, _, _ := s.Get(ctx, "org-a", "lt-1")
	b, _, _ := s.Get(ctx, "org-b", "lt-1")
	if a.Revision != "v1" || b.Revision != "v9" {
		t.Fatalf("tenants bleed: a=%+v b=%+v", a, b)
	}
	la, _ := s.List(ctx, "org-a")
	if len(la) != 1 {
		t.Fatalf("org-a list = %+v", la)
	}
}

func TestFactsRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, _, ok, err := s.GetFacts(ctx, "default", "lt-1"); err != nil || ok {
		t.Fatalf("empty facts = %v %v", ok, err)
	}
	facts := []byte(`{"cpu": "ryzen", "memory": 32}`)
	if err := s.PutFacts(ctx, "default", "lt-1", facts, t0); err != nil {
		t.Fatal(err)
	}
	got, at, ok, err := s.GetFacts(ctx, "default", "lt-1")
	if err != nil || !ok || !at.Equal(t0) {
		t.Fatalf("facts get = %v %v %v", at, ok, err)
	}
	if string(got) == "" {
		t.Fatal("facts empty")
	}
	// Update replaces.
	if err := s.PutFacts(ctx, "default", "lt-1", []byte(`{"cpu":"intel"}`), t0.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	got, at, _, _ = s.GetFacts(ctx, "default", "lt-1")
	if !at.Equal(t0.Add(time.Hour)) || string(got) == string(facts) {
		t.Fatal("facts not replaced")
	}
}

func TestConvergenceAggregate(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := t0.Add(10 * time.Minute)

	// Ring of 4: one on target+healthy, one on target+error, one on target
	// but stale, one on the old revision. Plus a foreign tenant row and a
	// non-member row that must not count.
	up := func(tag, rev, errmsg string, seen time.Time) {
		t.Helper()
		if _, err := s.Upsert(ctx, "default",
			observed.CheckIn{Tag: tag, Revision: rev, Phase: observed.Running, Error: errmsg}, seen); err != nil {
			t.Fatal(err)
		}
	}
	up("r-1", "v2", "", now.Add(-time.Minute))     // healthy
	up("r-2", "v2", "boom", now.Add(-time.Minute)) // on target, error
	up("r-3", "v2", "", now.Add(-time.Hour))       // on target, offline
	up("r-4", "v1", "", now.Add(-time.Minute))     // behind
	up("outsider", "v2", "", now.Add(-time.Minute))
	if _, err := s.Upsert(ctx, "org-x", observed.CheckIn{Tag: "r-1", Revision: "v2"}, now); err != nil {
		t.Fatalf("setup: %v", err)
	}

	conv := s.NewConvergence("default", func(group string) []string {
		if group == "ring0" {
			return []string{"r-1", "r-2", "r-3", "r-4", "r-5-never-seen"}
		}
		return nil
	})
	conv.Now = func() time.Time { return now }

	rs, err := conv.RingStatus(ctx, "ring0", "v2")
	if err != nil {
		t.Fatal(err)
	}
	if rs.Total != 5 || rs.OnTarget != 3 || rs.Healthy != 1 {
		t.Fatalf("ring = %+v, want total 5 onTarget 3 healthy 1", rs)
	}

	// Empty group: zero status, no error.
	rs, err = conv.RingStatus(ctx, "ghost", "v2")
	if err != nil || rs.Total != 0 {
		t.Fatalf("empty ring = %+v, %v", rs, err)
	}
}
