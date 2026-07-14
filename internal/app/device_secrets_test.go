package app

import (
	"context"
	"encoding/base64"
	"errors"
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

// otherSealer returns a Sealer built from a different key than testSealer, so
// tests can exercise the wrong-key reveal path.
func otherSealer(t *testing.T) secretbox.Sealer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(255 - i)
	}
	s, err := secretbox.New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

// TestDeviceSecretsRevealWrongKeyErrors (finding: Secret Reveal tamper/wrong-
// key path is untested): a secret sealed under one key must never open under
// another, and the failed reveal must not stamp MarkRevealed - a read that
// produced nothing must not appear in the audit trail as a successful reveal.
func TestDeviceSecretsRevealWrongKeyErrors(t *testing.T) {
	store := newMemSecretStore()
	sealed := NewDeviceSecretsService(store, testSealer(t), newFakeClock(testT0), "")
	ctx := context.Background()

	const pass = "z7Xq-9pLm-R2wK-v4N8"
	if err := sealed.Store(ctx, "lt-1", secret.LUKS, pass, "svc:station-1"); err != nil {
		t.Fatalf("store: %v", err)
	}

	// Reveal with a service wired to a DIFFERENT key: must error, return no
	// plaintext, and not mark the secret revealed.
	wrongKey := NewDeviceSecretsService(store, otherSealer(t), newFakeClock(testT0), "")
	got, ok, err := wrongKey.Reveal(ctx, "lt-1", secret.LUKS, "mallory@example.com")
	if err == nil {
		t.Fatal("reveal with the wrong key must error")
	}
	if got != "" {
		t.Fatalf("reveal with the wrong key returned plaintext: %q", got)
	}
	if !ok {
		t.Fatal("ok should report the secret exists, even though it could not be opened")
	}
	if m := store.meta[skey("lt-1", secret.LUKS)]; m.RevealedBy != "" || m.Revealed != "" {
		t.Fatalf("a failed reveal must not be audited as successful: %+v", m)
	}
}

// TestDeviceSecretsRevealTamperedCiphertextErrors: corrupting the stored
// ciphertext (bit flip) must fail authentication on Open, never return
// partial/garbage plaintext, and must not mark the secret revealed.
func TestDeviceSecretsRevealTamperedCiphertextErrors(t *testing.T) {
	store := newMemSecretStore()
	svc := NewDeviceSecretsService(store, testSealer(t), newFakeClock(testT0), "")
	ctx := context.Background()

	if err := svc.Store(ctx, "lt-1", secret.Admin, "s3cr3t-recovery", "svc:station-1"); err != nil {
		t.Fatalf("store: %v", err)
	}
	// Flip a bit in the stored ciphertext, simulating corruption.
	ct := store.ciph[skey("lt-1", secret.Admin)]
	ct[len(ct)-1] ^= 0xff

	got, ok, err := svc.Reveal(ctx, "lt-1", secret.Admin, "mallory@example.com")
	if err == nil {
		t.Fatal("reveal of a tampered ciphertext must error")
	}
	if got != "" {
		t.Fatalf("reveal of a tampered ciphertext returned plaintext: %q", got)
	}
	if !ok {
		t.Fatal("ok should report the secret exists, even though it could not be opened")
	}
	if m := store.meta[skey("lt-1", secret.Admin)]; m.RevealedBy != "" || m.Revealed != "" {
		t.Fatalf("a failed reveal must not be audited as successful: %+v", m)
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
	if err := svc.Store(context.Background(), "lt-1", secret.LUKS, "x", "svc"); !errors.Is(err, ports.ErrSealerDisabled) {
		t.Fatalf("store without a key = %v, want ErrSealerDisabled", err)
	}
}
