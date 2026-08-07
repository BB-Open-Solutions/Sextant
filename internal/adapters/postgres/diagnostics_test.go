package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"
)

// diagnostics_test.go covers the store behind the most revealing data the
// product holds: a sealed bundle of journal fragments from a member of
// staff's machine (design 0010).
//
// The retention window is enforced a layer up, in app.DiagnosticsService,
// which deletes an expired bundle on sight. What is tested here is the layer
// below it: that a re-request replaces rather than accumulates, and that
// every route out of this table is fenced by tenant. The DPIA leans on both.

func TestDiagnosticsRoundTrip(t *testing.T) {
	s := openStore(t).Diagnostics()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	sealed := []byte("sealed-bundle-bytes")

	if err := s.Put(ctx, "t1", "nuc-01", sealed, now); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, meta, ok, err := s.Get(ctx, "t1", "nuc-01")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if !bytes.Equal(got, sealed) {
		t.Errorf("ciphertext came back as %q, want %q", got, sealed)
	}
	if meta.Tag != "nuc-01" || meta.Size != len(sealed) {
		t.Errorf("meta = %+v, want tag nuc-01 and size %d", meta, len(sealed))
	}
	if !meta.Created.Equal(now) {
		t.Errorf("created = %v, want %v - the age decides expiry", meta.Created, now)
	}
}

func TestDiagnosticsRequestReplacesTheBundle(t *testing.T) {
	s := openStore(t).Diagnostics()
	ctx := context.Background()
	first := time.Now().UTC().Add(-time.Hour).Truncate(time.Millisecond)
	second := time.Now().UTC().Truncate(time.Millisecond)

	if err := s.Put(ctx, "t1", "nuc-01", []byte("old-and-longer"), first); err != nil {
		t.Fatal(err)
	}
	if err := s.Put(ctx, "t1", "nuc-01", []byte("new"), second); err != nil {
		t.Fatal(err)
	}

	// One row per device is the whole retention story: if a second request
	// appended instead of replacing, the first bundle would outlive its
	// window while the metadata reported the newer, still-valid timestamp.
	got, meta, ok, err := s.Get(ctx, "t1", "nuc-01")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if string(got) != "new" {
		t.Errorf("ciphertext = %q, want the replacement", got)
	}
	if !meta.Created.Equal(second) {
		t.Errorf("created = %v, want the newer stamp %v", meta.Created, second)
	}

	var rows int
	if err := s.s.pool.QueryRow(ctx,
		`SELECT count(*) FROM device_diagnostics WHERE tenant='t1' AND tag='nuc-01'`).
		Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d rows for one device, want 1 - bundles are accumulating", rows)
	}
}

func TestDiagnosticsIsFencedByTenant(t *testing.T) {
	s := openStore(t).Diagnostics()
	ctx := context.Background()
	now := time.Now().UTC()

	if err := s.Put(ctx, "t1", "nuc-01", []byte("theirs"), now); err != nil {
		t.Fatal(err)
	}

	// Every route out of this table, because one unfenced route is enough.
	if _, _, ok, err := s.Get(ctx, "t2", "nuc-01"); err != nil || ok {
		t.Errorf("Get crossed tenants (ok=%v, err=%v)", ok, err)
	}
	if _, ok, err := s.Meta(ctx, "t2", "nuc-01"); err != nil || ok {
		t.Errorf("Meta crossed tenants (ok=%v, err=%v)", ok, err)
	}
	if err := s.Delete(ctx, "t2", "nuc-01"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, _, ok, err := s.Get(ctx, "t1", "nuc-01"); err != nil || !ok {
		t.Errorf("a delete for another tenant took this bundle (ok=%v, err=%v)", ok, err)
	}
}

func TestDiagnosticsMetaReportsSizeWithoutTheBundle(t *testing.T) {
	s := openStore(t).Diagnostics()
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Millisecond)
	sealed := bytes.Repeat([]byte("x"), 4096)

	if err := s.Put(ctx, "t1", "nuc-01", sealed, now); err != nil {
		t.Fatal(err)
	}
	meta, ok, err := s.Meta(ctx, "t1", "nuc-01")
	if err != nil || !ok {
		t.Fatalf("meta: ok=%v err=%v", ok, err)
	}
	// The device page renders this. It must be the real size, or an operator
	// deciding whether to open a bundle is deciding on a guess.
	if meta.Size != len(sealed) {
		t.Errorf("size = %d, want %d", meta.Size, len(sealed))
	}
	if !meta.Created.Equal(now) {
		t.Errorf("created = %v, want %v", meta.Created, now)
	}
}

func TestDiagnosticsAbsentBundleReadsAsAbsent(t *testing.T) {
	s := openStore(t).Diagnostics()
	ctx := context.Background()

	// A device with no bundle is the normal case, not a failure - and the
	// expiry path deletes bundles behind the caller's back, so "gone" has to
	// be an ordinary answer on both routes.
	if _, _, ok, err := s.Get(ctx, "t1", "never"); err != nil || ok {
		t.Errorf("Get: ok=%v, err=%v, want absent and no error", ok, err)
	}
	if _, ok, err := s.Meta(ctx, "t1", "never"); err != nil || ok {
		t.Errorf("Meta: ok=%v, err=%v, want absent and no error", ok, err)
	}
	if err := s.Delete(ctx, "t1", "never"); err != nil {
		t.Errorf("deleting an absent bundle failed: %v", err)
	}
}
