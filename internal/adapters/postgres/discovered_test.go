package postgres

import (
	"context"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// TestDiscoveredReportRoundTripAndReplace covers the pre-enrollment store's
// core contract: a report is a whole-set REPLACE (not an upsert-merge), and
// List returns MAC-sorted results. Also exercises the batched write path
// (DELETE + all INSERTs in one pgx.Batch/SendBatch round trip).
func TestDiscoveredReportRoundTripAndReplace(t *testing.T) {
	s := openStore(t).Discovered()
	ctx := context.Background()

	if got, err := s.List(ctx, "t1", "nuc-1"); err != nil || len(got) != 0 {
		t.Fatalf("empty list = %+v %v", got, err)
	}

	first := []discovery.Discovered{
		{MAC: "bb:bb:bb:bb:bb:02", Vendor: "Lenovo", Phase: observed.Discovered, LastSeen: t0},
		{MAC: "aa:aa:aa:aa:aa:01", Vendor: "Dell", Phase: observed.Discovered, LastSeen: t0},
	}
	if err := s.Report(ctx, "t1", "nuc-1", first, t0); err != nil {
		t.Fatal(err)
	}
	got, err := s.List(ctx, "t1", "nuc-1")
	if err != nil || len(got) != 2 {
		t.Fatalf("list after report = %+v %v", got, err)
	}
	// MAC-sorted.
	if got[0].MAC != "aa:aa:aa:aa:aa:01" || got[1].MAC != "bb:bb:bb:bb:bb:02" {
		t.Fatalf("list not MAC-sorted: %+v", got)
	}
	if got[0].Vendor != "Dell" {
		t.Fatalf("fields not round-tripped: %+v", got[0])
	}

	// A second report REPLACES the whole set: the first MAC vanishes even
	// though it isn't in the new report.
	second := []discovery.Discovered{
		{MAC: "cc:cc:cc:cc:cc:03", Vendor: "HP", Phase: observed.Discovered, LastSeen: t0},
	}
	if err := s.Report(ctx, "t1", "nuc-1", second, t0); err != nil {
		t.Fatal(err)
	}
	got, err = s.List(ctx, "t1", "nuc-1")
	if err != nil || len(got) != 1 || got[0].MAC != "cc:cc:cc:cc:cc:03" {
		t.Fatalf("list after replace = %+v %v", got, err)
	}
}

// TestDiscoveredReportAtomicOnBatchError proves the batched write stays
// transactional: a report whose batch fails partway (here, a duplicate MAC
// violating the (tenant,station,mac) primary key) must leave the prior
// discovered set completely untouched, not half-replaced by whichever
// INSERTs ran before the failing one.
func TestDiscoveredReportAtomicOnBatchError(t *testing.T) {
	s := openStore(t).Discovered()
	ctx := context.Background()

	prior := []discovery.Discovered{
		{MAC: "aa:aa:aa:aa:aa:01", Vendor: "Dell", Phase: observed.Discovered, LastSeen: t0},
	}
	if err := s.Report(ctx, "t1", "nuc-1", prior, t0); err != nil {
		t.Fatal(err)
	}

	bad := []discovery.Discovered{
		{MAC: "dd:dd:dd:dd:dd:04", Vendor: "Asus", Phase: observed.Discovered, LastSeen: t0},
		{MAC: "dd:dd:dd:dd:dd:04", Vendor: "Asus-duplicate", Phase: observed.Discovered, LastSeen: t0},
	}
	if err := s.Report(ctx, "t1", "nuc-1", bad, t0); err == nil {
		t.Fatal("duplicate-MAC report accepted, want a primary-key violation")
	}

	// The prior set survives untouched - the DELETE and every queued INSERT
	// rolled back together.
	got, err := s.List(ctx, "t1", "nuc-1")
	if err != nil || len(got) != 1 || got[0].MAC != "aa:aa:aa:aa:aa:01" {
		t.Fatalf("list after failed report = %+v %v, want the untouched prior set", got, err)
	}
}

// TestDiscoveredRemoveAndTenantIsolation covers Remove and that a report for
// one tenant/station never touches another's set.
func TestDiscoveredRemoveAndTenantIsolation(t *testing.T) {
	s := openStore(t).Discovered()
	ctx := context.Background()

	devs := []discovery.Discovered{
		{MAC: "aa:aa:aa:aa:aa:01", Phase: observed.Discovered, LastSeen: t0},
		{MAC: "bb:bb:bb:bb:bb:02", Phase: observed.Discovered, LastSeen: t0},
	}
	if err := s.Report(ctx, "t1", "nuc-1", devs, t0); err != nil {
		t.Fatal(err)
	}
	if err := s.Report(ctx, "t2", "nuc-1", devs, t0); err != nil {
		t.Fatal(err)
	}

	if err := s.Remove(ctx, "t1", "nuc-1", "aa:aa:aa:aa:aa:01"); err != nil {
		t.Fatal(err)
	}
	got, _ := s.List(ctx, "t1", "nuc-1")
	if len(got) != 1 || got[0].MAC != "bb:bb:bb:bb:bb:02" {
		t.Fatalf("t1 after remove = %+v", got)
	}
	// t2 (same station name, different tenant) is unaffected.
	got2, _ := s.List(ctx, "t2", "nuc-1")
	if len(got2) != 2 {
		t.Fatalf("t2 list = %+v, want the untouched pair (tenant isolation)", got2)
	}
}
