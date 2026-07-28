package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
)

// escrowStore is a minimal in-memory ports.DeviceSecretStore.
type escrowStore struct {
	ciph map[string][]byte
	meta map[string]secret.Meta
}

func newEscrowStore() *escrowStore {
	return &escrowStore{ciph: map[string][]byte{}, meta: map[string]secret.Meta{}}
}

func ekey(tag string, k secret.Kind) string { return tag + "|" + string(k) }

func (s *escrowStore) Put(_ context.Context, _, tag string, kind secret.Kind, ciphertext []byte, createdBy string, now time.Time) error {
	s.ciph[ekey(tag, kind)] = ciphertext
	s.meta[ekey(tag, kind)] = secret.Meta{Tag: tag, Kind: kind, CreatedBy: createdBy, Created: now.UTC().Format(time.RFC3339)}
	return nil
}

func (s *escrowStore) Get(_ context.Context, _, tag string, kind secret.Kind) ([]byte, secret.Meta, bool, error) {
	c, ok := s.ciph[ekey(tag, kind)]
	if !ok {
		return nil, secret.Meta{}, false, nil
	}
	return c, s.meta[ekey(tag, kind)], true, nil
}

func (s *escrowStore) List(_ context.Context, _, tag string) ([]secret.Meta, error) {
	var out []secret.Meta
	for _, m := range s.meta {
		if m.Tag == tag {
			out = append(out, m)
		}
	}
	return out, nil
}

func (s *escrowStore) MarkRevealed(_ context.Context, _, tag string, kind secret.Kind, revealedBy string, now time.Time) error {
	m := s.meta[ekey(tag, kind)]
	m.RevealedBy, m.Revealed = revealedBy, now.UTC().Format(time.RFC3339)
	s.meta[ekey(tag, kind)] = m
	return nil
}

func escrowService(t *testing.T, store *escrowStore) *app.DeviceSecretsService {
	t.Helper()
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	sealer, err := secretbox.New(base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatal(err)
	}
	return app.NewDeviceSecretsService(store, sealer, fixedClock{time.Now()}, "")
}

// A check-in carrying a provisioning-minted recovery key (design 0009) is
// sealed into the device-secret store and confirmed via the response header;
// the store holds ciphertext, never the plaintext.
func TestCheckinEscrowsRecoveryKey(t *testing.T) {
	fo := newFakeObserved()
	inv := app.NewInventoryService(fo, fo, fixedClock{time.Now()}, "")
	store := newEscrowStore()
	svc := escrowService(t, store)
	mux := http.NewServeMux()
	NewCheckin(inv, nil, "tok").WithDeviceSecrets(svc).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	const pass = "modheks-recovery-phrase"
	req, _ := http.NewRequest("POST", srv.URL+"/api/checkin",
		strings.NewReader(`{"tag":"lt-1","revision":"r1","phase":"running","recoveryKey":"`+pass+`"}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("X-Recovery-Key-Stored") != "1" {
		t.Fatal("missing X-Recovery-Key-Stored confirmation header")
	}
	if raw := string(store.ciph[ekey("lt-1", secret.LUKS)]); raw == "" || strings.Contains(raw, pass) {
		t.Fatal("stored value is empty or plaintext - must be sealed")
	}
	got, ok, err := svc.Reveal(context.Background(), "lt-1", secret.LUKS, "test")
	if err != nil || !ok || got != pass {
		t.Fatalf("reveal = %q ok=%v err=%v, want %q", got, ok, err, pass)
	}
}

// Without a configured store the beat still succeeds but the confirmation
// header stays absent, so the device keeps its copy and retries - recovery
// material is never silently dropped.
func TestCheckinRecoveryKeyWithoutStoreIsNotAcked(t *testing.T) {
	srv, _ := newCheckinServer(t, "tok")
	req, _ := http.NewRequest("POST", srv.URL+"/api/checkin",
		strings.NewReader(`{"tag":"lt-1","revision":"r1","phase":"running","recoveryKey":"abc"}`))
	req.Header.Set("Authorization", "Bearer tok")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", resp.StatusCode)
	}
	if resp.Header.Get("X-Recovery-Key-Stored") != "" {
		t.Fatal("no store configured, yet the server claimed to have sealed the key")
	}
}

// An oversized value cannot be a systemd-cryptenroll recovery phrase; the
// beat is refused outright rather than sealing junk.
func TestCheckinOversizedRecoveryKeyRefused(t *testing.T) {
	srv, _ := newCheckinServer(t, "tok")
	big := strings.Repeat("x", 300)
	code := post(t, srv.URL+"/api/checkin", "tok",
		`{"tag":"lt-1","revision":"r1","phase":"running","recoveryKey":"`+big+`"}`)
	if code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", code)
	}
}
