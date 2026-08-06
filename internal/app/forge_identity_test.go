package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
)

// memForge is an in-memory ports.ForgeIdentityStore.
type memForge struct {
	id  forge.Identity
	has bool
}

func (m *memForge) GetForgeIdentity(context.Context, string) (forge.Identity, bool, error) {
	return m.id, m.has, nil
}
func (m *memForge) PutForgeIdentity(_ context.Context, _ string, id forge.Identity) error {
	m.id, m.has = id, true
	return nil
}
func (m *memForge) DeleteForgeIdentity(context.Context, string) error {
	m.id, m.has = forge.Identity{}, false
	return nil
}

// newForgeSvc builds a service over a temp home with a real sealer.
func newForgeSvc(t *testing.T) (*ForgeIdentityService, *memForge, string) {
	t.Helper()
	sealer, err := secretbox.New(base64.StdEncoding.EncodeToString(bytes.Repeat([]byte("k"), 32)))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	if !sealer.Enabled() {
		t.Fatal("precondition: the test sealer is disabled, so nothing below proves anything")
	}
	store := &memForge{}
	path := filepath.Join(t.TempDir(), ".netrc")
	return NewForgeIdentityService(store, sealer, "default", path, nil), store, path
}

// TestForgeCredentialRotationIsWhatGitReads is the whole point of the
// feature: an admin sets a credential and the file git authenticates from
// carries it, without a restart.
func TestForgeCredentialRotationIsWhatGitReads(t *testing.T) {
	svc, store, path := newForgeSvc(t)
	ctx := context.Background()

	if err := svc.Set(ctx, "forge.example.org", "sextant-bot", "tok-first", "bram"); err != nil {
		t.Fatalf("set: %v", err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read netrc: %v", err)
	}
	if got := string(b); got != "machine forge.example.org login sextant-bot password tok-first\n" {
		t.Fatalf("netrc = %q", got)
	}
	// Mode matters: a token readable by anything else on the volume is the
	// disclosure this feature exists to avoid.
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("netrc mode = %v, want 0600", fi.Mode().Perm())
	}

	// Rotating replaces it rather than appending: two machine lines for one
	// host would leave git using whichever came first, so a rotation that
	// appended would look like it worked and change nothing.
	if err := svc.Set(ctx, "forge.example.org", "sextant-bot", "tok-second", "bram"); err != nil {
		t.Fatalf("rotate: %v", err)
	}
	b, _ = os.ReadFile(path)
	if got := string(b); got != "machine forge.example.org login sextant-bot password tok-second\n" {
		t.Fatalf("after rotation netrc = %q", got)
	}

	// The token never comes back out.
	cur, ok, err := svc.Current(ctx)
	if err != nil || !ok {
		t.Fatalf("current: ok=%v err=%v", ok, err)
	}
	if cur.TokenEnc != nil {
		t.Error("Current returned token material; it must never leave the service")
	}
	if cur.Username != "sextant-bot" || cur.UpdatedBy != "bram" {
		t.Errorf("current = %+v", cur)
	}
	// What IS stored is sealed, not the token.
	if strings.Contains(string(store.id.TokenEnc), "tok-second") {
		t.Error("the stored blob contains the plaintext token")
	}
}

// TestForgeApplyRestoresTheStoredCredential covers the restart path: a new
// pod gets a fresh volume section and must write the netrc from the store.
func TestForgeApplyRestoresTheStoredCredential(t *testing.T) {
	svc, _, path := newForgeSvc(t)
	ctx := context.Background()
	if err := svc.Set(ctx, "forge.example.org", "bot", "tok", "bram"); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	wrote, err := svc.Apply(ctx)
	if err != nil || !wrote {
		t.Fatalf("apply: wrote=%v err=%v", wrote, err)
	}
	b, _ := os.ReadFile(path)
	if !strings.Contains(string(b), "password tok") {
		t.Fatalf("netrc after apply = %q", b)
	}
}

// TestForgeApplyWithNothingStoredLeavesTheMountedCredentialAlone: an upgrade
// must not delete or overwrite the netrc a deployment already mounts.
func TestForgeApplyWithNothingStoredLeavesTheMountedCredentialAlone(t *testing.T) {
	svc, _, path := newForgeSvc(t)
	mounted := "machine forge.example.org login mounted password from-the-secret\n"
	if err := os.WriteFile(path, []byte(mounted), 0o600); err != nil {
		t.Fatal(err)
	}
	wrote, err := svc.Apply(context.Background())
	if err != nil || wrote {
		t.Fatalf("apply with empty store: wrote=%v err=%v", wrote, err)
	}
	b, _ := os.ReadFile(path)
	if string(b) != mounted {
		t.Fatalf("the mounted credential was disturbed: %q", b)
	}
}

// TestForgeClearFallsBackToTheMount: clearing removes the console's own file
// so the mounted credential (if any) governs again.
func TestForgeClearFallsBackToTheMount(t *testing.T) {
	svc, store, path := newForgeSvc(t)
	ctx := context.Background()
	if err := svc.Set(ctx, "forge.example.org", "bot", "tok", "bram"); err != nil {
		t.Fatal(err)
	}
	if err := svc.Clear(ctx, "bram"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if store.has {
		t.Error("the identity survived Clear")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("netrc still present after Clear: %v", err)
	}
	// Clearing twice is not an error: the file is already gone.
	if err := svc.Clear(ctx, "bram"); err != nil {
		t.Errorf("second clear: %v", err)
	}
}

func TestForgeValidateRefusesWhatWouldBreakTheNetrcLine(t *testing.T) {
	cases := []struct{ name, host, user, token string }{
		{"host as URL", "https://forge.example.org", "bot", "tok"},
		{"host with path", "forge.example.org/dawo", "bot", "tok"},
		{"host with space", "forge example.org", "bot", "tok"},
		{"empty host", "", "bot", "tok"},
		{"empty user", "forge.example.org", "", "tok"},
		{"empty token", "forge.example.org", "bot", ""},
		{"token with newline", "forge.example.org", "bot", "tok\nmachine evil.example password x"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := forge.Validate(c.host, c.user, c.token); err == nil {
				t.Errorf("Validate accepted %q/%q/%q", c.host, c.user, c.token)
			}
		})
	}
	if err := forge.Validate("forge.example.org", "sextant-bot", "tok"); err != nil {
		t.Errorf("Validate refused a good identity: %v", err)
	}
}

// TestForgeSetRefusedWithoutASealingKey: no key means nowhere safe to put the
// token, and the caller must learn that rather than get a silent no-op.
func TestForgeSetRefusedWithoutASealingKey(t *testing.T) {
	sealer, err := secretbox.New("")
	if err != nil {
		t.Fatal(err)
	}
	svc := NewForgeIdentityService(&memForge{}, sealer, "default",
		filepath.Join(t.TempDir(), ".netrc"), nil)
	if svc.Enabled() {
		t.Fatal("precondition: service claims to be enabled without a key")
	}
	if err := svc.Set(context.Background(), "forge.example.org", "bot", "tok", "bram"); err == nil {
		t.Error("Set succeeded without a sealing key")
	}
}
