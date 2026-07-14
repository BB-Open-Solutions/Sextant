package app

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memSecretStore is an in-memory ports.DeviceSecretStore for service tests.
type memSecretStore struct {
	ciph map[string][]byte
	meta map[string]secret.Meta
}

func newMemSecretStore() *memSecretStore {
	return &memSecretStore{ciph: map[string][]byte{}, meta: map[string]secret.Meta{}}
}

func skey(tag string, k secret.Kind) string { return tag + "|" + string(k) }

func (s *memSecretStore) Put(_ context.Context, _, tag string, kind secret.Kind, ciphertext []byte, createdBy string, now time.Time) error {
	s.ciph[skey(tag, kind)] = ciphertext
	s.meta[skey(tag, kind)] = secret.Meta{Tag: tag, Kind: kind, CreatedBy: createdBy, Created: now.UTC().Format(time.RFC3339)}
	return nil
}
func (s *memSecretStore) Get(_ context.Context, _, tag string, kind secret.Kind) ([]byte, secret.Meta, bool, error) {
	c, ok := s.ciph[skey(tag, kind)]
	if !ok {
		return nil, secret.Meta{}, false, nil
	}
	return c, s.meta[skey(tag, kind)], true, nil
}
func (s *memSecretStore) List(_ context.Context, _, tag string) ([]secret.Meta, error) {
	var out []secret.Meta
	for k, m := range s.meta {
		if len(k) > len(tag) && k[:len(tag)] == tag {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *memSecretStore) MarkRevealed(_ context.Context, _, tag string, kind secret.Kind, revealedBy string, now time.Time) error {
	m := s.meta[skey(tag, kind)]
	m.RevealedBy, m.Revealed = revealedBy, now.UTC().Format(time.RFC3339)
	s.meta[skey(tag, kind)] = m
	return nil
}

func TestDeviceSecretsStoreAndReveal(t *testing.T) {
	store := newMemSecretStore()
	svc := NewDeviceSecretsService(store, testSealer(t), newFakeClock(testT0), "")
	ctx := context.Background()

	if !svc.Enabled() {
		t.Fatal("service with a store and a key must be enabled")
	}

	// Store seals; the store holds ciphertext, not plaintext.
	const pass = "z7Xq-9pLm-R2wK-v4N8"
	if err := svc.Store(ctx, "lt-1", secret.LUKS, pass, "svc:station-1"); err != nil {
		t.Fatalf("store: %v", err)
	}
	if raw := string(store.ciph[skey("lt-1", secret.LUKS)]); raw == "" || raw == pass {
		t.Fatal("stored value is empty or plaintext - must be sealed")
	}

	// Reveal unseals and stamps the reader.
	got, ok, err := svc.Reveal(ctx, "lt-1", secret.LUKS, "alice@example.com")
	if err != nil || !ok {
		t.Fatalf("reveal: ok=%v err=%v", ok, err)
	}
	if got != pass {
		t.Fatalf("revealed %q, want %q", got, pass)
	}
	if m := store.meta[skey("lt-1", secret.LUKS)]; m.RevealedBy != "alice@example.com" || m.Revealed == "" {
		t.Fatalf("reveal not audited: %+v", m)
	}

	// A missing secret reveals ok=false, no error.
	if _, ok, err := svc.Reveal(ctx, "lt-1", secret.Admin, "alice@example.com"); ok || err != nil {
		t.Fatalf("missing secret: ok=%v err=%v", ok, err)
	}

	// Metadata lists without plaintext.
	metas, err := svc.List(ctx, "lt-1")
	if err != nil || len(metas) != 1 || metas[0].Kind != secret.LUKS {
		t.Fatalf("list = %v (err %v)", metas, err)
	}
}

func TestDeviceSecretsRefusesBadInput(t *testing.T) {
	svc := NewDeviceSecretsService(newMemSecretStore(), testSealer(t), newFakeClock(testT0), "")
	ctx := context.Background()
	if err := svc.Store(ctx, "lt-1", secret.Kind("tpm"), "x", "svc"); err == nil {
		t.Error("unknown kind must be rejected")
	}
	if err := svc.Store(ctx, "lt-1", secret.LUKS, "", "svc"); err == nil {
		t.Error("empty plaintext must be rejected")
	}
}

func TestDeviceSecretsDisabledWithoutKey(t *testing.T) {
	svc := NewDeviceSecretsService(newMemSecretStore(), secretbox.Sealer{}, newFakeClock(testT0), "")
	if svc.Enabled() {
		t.Fatal("service without a key must be disabled")
	}
	if err := svc.Store(context.Background(), "lt-1", secret.LUKS, "x", "svc"); err != ports.ErrSealerDisabled {
		t.Fatalf("store without a key = %v, want ErrSealerDisabled", err)
	}
}
