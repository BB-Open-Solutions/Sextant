package app

import (
	"context"
	"errors"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memDiagStore is an in-memory ports.DiagnosticsStore.
type memDiagStore struct {
	ciph    map[string][]byte
	created map[string]time.Time
}

func newMemDiagStore() *memDiagStore {
	return &memDiagStore{ciph: map[string][]byte{}, created: map[string]time.Time{}}
}

func (s *memDiagStore) Put(_ context.Context, _, tag string, ciphertext []byte, now time.Time) error {
	s.ciph[tag], s.created[tag] = ciphertext, now
	return nil
}

func (s *memDiagStore) Get(_ context.Context, _, tag string) ([]byte, ports.DiagnosticsMeta, bool, error) {
	c, ok := s.ciph[tag]
	if !ok {
		return nil, ports.DiagnosticsMeta{}, false, nil
	}
	return c, ports.DiagnosticsMeta{Tag: tag, Size: len(c), Created: s.created[tag]}, true, nil
}

func (s *memDiagStore) Meta(_ context.Context, _, tag string) (ports.DiagnosticsMeta, bool, error) {
	c, ok := s.ciph[tag]
	if !ok {
		return ports.DiagnosticsMeta{}, false, nil
	}
	return ports.DiagnosticsMeta{Tag: tag, Size: len(c), Created: s.created[tag]}, true, nil
}

func (s *memDiagStore) Delete(_ context.Context, _, tag string) error {
	delete(s.ciph, tag)
	delete(s.created, tag)
	return nil
}

func TestDiagnosticsSealRetentionAndRetire(t *testing.T) {
	store := newMemDiagStore()
	clock := newFakeClock(testT0)
	svc := NewDiagnosticsService(store, testSealer(t), clock, "")
	ctx := context.Background()
	bundle := []byte("gzip-bytes-with-journal-tail")

	if err := svc.Put(ctx, "lt-1", bundle); err != nil {
		t.Fatalf("put: %v", err)
	}
	if raw := string(store.ciph["lt-1"]); raw == string(bundle) {
		t.Fatal("stored bundle is plaintext - must be sealed")
	}
	got, meta, ok, err := svc.Get(ctx, "lt-1")
	if err != nil || !ok || string(got) != string(bundle) {
		t.Fatalf("get = %q ok=%v err=%v", got, ok, err)
	}
	if meta.Size == 0 || !meta.Created.Equal(testT0) {
		t.Fatalf("meta = %+v", meta)
	}

	// Within retention the bundle stays; past it, it reads absent AND is
	// deleted on sight - retention needs no sweeper.
	clock.Advance(DiagnosticsRetention - time.Hour)
	if _, ok, _ := svc.Meta(ctx, "lt-1"); !ok {
		t.Fatal("bundle vanished inside the retention window")
	}
	clock.Advance(2 * time.Hour)
	if _, _, ok, _ := svc.Get(ctx, "lt-1"); ok {
		t.Fatal("expired bundle still served")
	}
	if _, ok := store.ciph["lt-1"]; ok {
		t.Fatal("expired bundle not deleted from the store")
	}

	// Size bounds refuse junk.
	if err := svc.Put(ctx, "lt-1", nil); err == nil {
		t.Fatal("empty bundle accepted")
	}
	if err := svc.Put(ctx, "lt-1", make([]byte, MaxDiagnosticsBundle+1)); err == nil {
		t.Fatal("oversized bundle accepted")
	}

	// Delete is nil-safe (kill-switched deployment).
	var off *DiagnosticsService
	if err := off.Delete(ctx, "lt-1"); err != nil {
		t.Fatalf("nil-service delete: %v", err)
	}
}

// failDeleteDiagStore refuses deletions, which is the case that used to be
// invisible: the delete error was discarded and the caller was told the bundle
// was gone.
type failDeleteDiagStore struct {
	*memDiagStore
	deletes int
}

func (s *failDeleteDiagStore) Delete(context.Context, string, string) error {
	s.deletes++
	return errors.New("store refuses to delete")
}

// TestExpiredBundleThatCannotBeDeletedIsNotReportedGone: retention here is
// enforced at every exit and by no sweeper, so a failed delete is never
// retried by anything. Reporting "no bundle" would tell an operator that a
// device's journal - personal data - is gone while it is still stored, and
// nothing anywhere would ever contradict that.
//
// This is the class of defect the abandoned-branch orphan belonged to, found
// by looking for the same discarded-error shape rather than by another
// accident.
func TestExpiredBundleThatCannotBeDeletedIsNotReportedGone(t *testing.T) {
	store := &failDeleteDiagStore{memDiagStore: newMemDiagStore()}
	clock := newFakeClock(testT0)
	svc := NewDiagnosticsService(store, testSealer(t), clock, "")
	ctx := context.Background()

	if err := svc.Put(ctx, "lt-1", []byte("journal")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(DiagnosticsRetention + time.Hour)

	if _, _, ok, err := svc.Get(ctx, "lt-1"); err == nil {
		t.Fatalf("Get reported the bundle as absent (ok=%v) while the delete failed", ok)
	}
	if _, ok, err := svc.Meta(ctx, "lt-1"); err == nil {
		t.Fatalf("Meta reported the bundle as absent (ok=%v) while the delete failed", ok)
	}
	if store.deletes != 2 {
		t.Fatalf("deletes attempted = %d, want one per read", store.deletes)
	}
	// And the ciphertext is genuinely still there - the whole reason the
	// answer had to change.
	if _, still := store.ciph["lt-1"]; !still {
		t.Fatal("test is not exercising what it claims: the bundle did get deleted")
	}
}

// TestExpiredBundleIsDeletedAndReadsAbsent: the ordinary path is unchanged - a
// successful expiry still reads as "no bundle", not as an error.
func TestExpiredBundleIsDeletedAndReadsAbsent(t *testing.T) {
	store := newMemDiagStore()
	clock := newFakeClock(testT0)
	svc := NewDiagnosticsService(store, testSealer(t), clock, "")
	ctx := context.Background()

	if err := svc.Put(ctx, "lt-1", []byte("journal")); err != nil {
		t.Fatal(err)
	}
	clock.Advance(DiagnosticsRetention + time.Hour)

	_, _, ok, err := svc.Get(ctx, "lt-1")
	if err != nil || ok {
		t.Fatalf("expired read = ok:%v err:%v, want absent and no error", ok, err)
	}
	if _, still := store.ciph["lt-1"]; still {
		t.Fatal("retention not enforced: the ciphertext is still in the store")
	}
}
