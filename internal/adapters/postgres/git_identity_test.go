package postgres

import (
	"bytes"
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
)

// git_identity_test.go covers the console's own forge credential.
//
// This is the row that decides who the audit trail says made a change. The
// processing agreement states the console pushes as a machine account; that
// claim is only true while this store holds the right row and hands it back
// unchanged.

func TestForgeIdentityRoundTripsSealed(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	sealed := []byte{0x00, 0x01, 0xff, 0xfe, 0x00} // opaque bytes, NUL included

	id := forge.Identity{
		Host:      "forgejo.bb-open.com",
		Username:  "sextant-console",
		TokenEnc:  sealed,
		UpdatedBy: "ada",
	}
	if err := s.PutForgeIdentity(ctx, "t1", id); err != nil {
		t.Fatalf("put: %v", err)
	}

	got, ok, err := s.GetForgeIdentity(ctx, "t1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Host != id.Host || got.Username != id.Username || got.UpdatedBy != id.UpdatedBy {
		t.Errorf("identity = %+v, want host/user/by from %+v", got, id)
	}
	// The token is a sealed blob, and bytea is the only column type that
	// survives a NUL. A text column would truncate here and the failure
	// would surface as an authentication error on the next push instead.
	if !bytes.Equal(got.TokenEnc, sealed) {
		t.Errorf("token = %v, want %v", got.TokenEnc, sealed)
	}
	if got.Updated.IsZero() {
		t.Error("updated is zero; the store stamps it and the console shows it")
	}
}

func TestForgeIdentityRotationReplaces(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	first := forge.Identity{Host: "old.example.org", Username: "old", TokenEnc: []byte("a"), UpdatedBy: "ada"}
	if err := s.PutForgeIdentity(ctx, "t1", first); err != nil {
		t.Fatal(err)
	}
	before, _, err := s.GetForgeIdentity(ctx, "t1")
	if err != nil {
		t.Fatal(err)
	}

	// Rotation is the point of this store: a revoked token must stop being
	// reachable, not sit beside its replacement.
	time.Sleep(2 * time.Millisecond)
	second := forge.Identity{Host: "forgejo.bb-open.com", Username: "sextant-console", TokenEnc: []byte("b"), UpdatedBy: "grace"}
	if err := s.PutForgeIdentity(ctx, "t1", second); err != nil {
		t.Fatal(err)
	}

	got, ok, err := s.GetForgeIdentity(ctx, "t1")
	if err != nil || !ok {
		t.Fatalf("get: ok=%v err=%v", ok, err)
	}
	if got.Username != "sextant-console" || string(got.TokenEnc) != "b" {
		t.Errorf("identity = %+v, want the rotated credential", got)
	}
	if got.UpdatedBy != "grace" {
		t.Errorf("updated_by = %q, want the person who rotated it", got.UpdatedBy)
	}
	if !got.Updated.After(before.Updated) {
		t.Errorf("updated %v is not after %v; the console would show a stale rotation date",
			got.Updated, before.Updated)
	}

	var rows int
	if err := s.pool.QueryRow(ctx, `SELECT count(*) FROM git_identity WHERE tenant='t1'`).Scan(&rows); err != nil {
		t.Fatal(err)
	}
	if rows != 1 {
		t.Errorf("%d identities for one tenant, want 1 - the old token is still stored", rows)
	}
}

func TestForgeIdentityIsPerTenant(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.PutForgeIdentity(ctx, "t1",
		forge.Identity{Host: "h", Username: "u", TokenEnc: []byte("secret"), UpdatedBy: "ada"}); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetForgeIdentity(ctx, "t2"); err != nil || ok {
		t.Errorf("another tenant read this credential (ok=%v, err=%v)", ok, err)
	}
	if err := s.DeleteForgeIdentity(ctx, "t2"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, ok, err := s.GetForgeIdentity(ctx, "t1"); err != nil || !ok {
		t.Errorf("another tenant's delete took this credential (ok=%v, err=%v)", ok, err)
	}
}

func TestForgeIdentityAbsentIsNotAnError(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// No identity is the default state: the deployment then falls back to
	// whatever credential is mounted, so absence has to be an ordinary
	// answer rather than a failure that blocks startup.
	if _, ok, err := s.GetForgeIdentity(ctx, "t1"); err != nil || ok {
		t.Errorf("get: ok=%v, err=%v, want absent and no error", ok, err)
	}
	if err := s.DeleteForgeIdentity(ctx, "t1"); err != nil {
		t.Errorf("deleting an absent identity failed: %v", err)
	}
}
