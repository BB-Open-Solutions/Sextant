package app

import (
	"context"
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
