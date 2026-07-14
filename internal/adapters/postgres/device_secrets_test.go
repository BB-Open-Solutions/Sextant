package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

func TestDeviceSecretsPutGetListMarkRevealed(t *testing.T) {
	s := openStore(t).DeviceSecrets()
	ctx := context.Background()

	if _, _, ok, err := s.Get(ctx, "t1", "lt-1", secret.LUKS); err != nil || ok {
		t.Fatalf("empty get = %v %v", ok, err)
	}

	// Put -> Get round trip: ciphertext and metadata come back intact, and a
	// freshly stored secret reads as never revealed.
	cipher := []byte("sealed-luks-recovery-passphrase")
	if err := s.Put(ctx, "t1", "lt-1", secret.LUKS, cipher, "alice@example.com", t0); err != nil {
		t.Fatal(err)
	}
	got, meta, ok, err := s.Get(ctx, "t1", "lt-1", secret.LUKS)
	if err != nil || !ok {
		t.Fatalf("get = %v %v", ok, err)
	}
	if string(got) != string(cipher) {
		t.Fatalf("ciphertext round trip = %q, want %q", got, cipher)
	}
	if meta.CreatedBy != "alice@example.com" || meta.Kind != secret.LUKS || meta.Tag != "lt-1" {
		t.Fatalf("meta = %+v", meta)
	}
	if meta.EverRevealed() {
		t.Fatalf("freshly stored secret must read as never revealed: %+v", meta)
	}

	// List returns metadata only, never ciphertext, and covers every kind
	// stored for the device.
	if err := s.Put(ctx, "t1", "lt-1", secret.Admin, []byte("sealed-admin-pw"), "alice@example.com", t0); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "t1", "lt-1")
	if err != nil || len(list) != 2 {
		t.Fatalf("list = %+v, %v", list, err)
	}
	if list[0].Kind != secret.Admin || list[1].Kind != secret.LUKS {
		t.Fatalf("list not ordered by kind: %+v", list)
	}

	// MarkRevealed stamps who read it and when.
	revealedAt := t0.Add(time.Hour)
	if err := s.MarkRevealed(ctx, "t1", "lt-1", secret.LUKS, "bob@example.com", revealedAt); err != nil {
		t.Fatal(err)
	}
	_, meta, _, err = s.Get(ctx, "t1", "lt-1", secret.LUKS)
	if err != nil {
		t.Fatal(err)
	}
	if meta.RevealedBy != "bob@example.com" || !meta.EverRevealed() {
		t.Fatalf("reveal not recorded: %+v", meta)
	}
	if meta.Revealed != revealedAt.UTC().Format(time.RFC3339) {
		t.Fatalf("revealed timestamp = %q, want %q", meta.Revealed, revealedAt.UTC().Format(time.RFC3339))
	}

	// Put after a reveal (rotation) clears the revealed marker again, so a
	// freshly rotated secret does not read as already seen.
	if err := s.Put(ctx, "t1", "lt-1", secret.LUKS, []byte("new-sealed-value"), "carol@example.com", t0.Add(2*time.Hour)); err != nil {
		t.Fatal(err)
	}
	rotatedCipher, meta, _, err := s.Get(ctx, "t1", "lt-1", secret.LUKS)
	if err != nil {
		t.Fatal(err)
	}
	if string(rotatedCipher) != "new-sealed-value" {
		t.Fatalf("rotated ciphertext = %q", rotatedCipher)
	}
	if meta.EverRevealed() || meta.RevealedBy != "" || meta.CreatedBy != "carol@example.com" {
		t.Fatalf("rotation did not reset reveal state: %+v", meta)
	}
}

func TestDeviceSecretsTenantIsolation(t *testing.T) {
	s := openStore(t).DeviceSecrets()
	ctx := context.Background()

	if err := s.Put(ctx, "org-a", "lt-1", secret.LUKS, []byte("a-secret"), "alice", t0); err != nil {
		t.Fatal(err)
	}

	// A tag collision under a different tenant must not see tenant A's
	// secret via Get or List: kind alone does not identify the row.
	if _, _, ok, err := s.Get(ctx, "org-b", "lt-1", secret.LUKS); err != nil || ok {
		t.Fatalf("cross-tenant get = %v %v, want not found", ok, err)
	}
	list, err := s.List(ctx, "org-b", "lt-1")
	if err != nil || len(list) != 0 {
		t.Fatalf("cross-tenant list = %+v, %v, want empty", list, err)
	}

	// The owning tenant still sees it.
	if _, _, ok, err := s.Get(ctx, "org-a", "lt-1", secret.LUKS); err != nil || !ok {
		t.Fatalf("owning tenant get = %v %v", ok, err)
	}
}
