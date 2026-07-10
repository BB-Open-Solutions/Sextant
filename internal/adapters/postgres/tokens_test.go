package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

func TestTokenStoreRoundTrip(t *testing.T) {
	s := openStore(t)
	ts := s.Tokens()
	ctx := context.Background()

	tok, _, err := token.Mint("ci-1", "CI token", token.Personal, "sub-ada",
		[]string{"editors"}, "viewer", t0, 24*time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	if err := ts.Put(ctx, tok); err != nil {
		t.Fatal(err)
	}

	got, ok, err := ts.Get(ctx, "ci-1")
	if err != nil || !ok {
		t.Fatalf("get = %v %v", ok, err)
	}
	if got.Name != "CI token" || got.Subject != "sub-ada" ||
		got.Ceiling != "viewer" || len(got.Groups) != 1 || got.Hash != tok.Hash {
		t.Fatalf("round trip lost data: %+v", got)
	}
	if !got.Expires.Equal(tok.Expires) {
		t.Errorf("expiry drift: %v vs %v", got.Expires, tok.Expires)
	}

	// List by subject.
	other, _, _ := token.Mint("ci-2", "n", token.Personal, "sub-bob", nil, "", t0, time.Hour)
	_ = ts.Put(ctx, other)
	mine, err := ts.ListBySubject(ctx, "sub-ada")
	if err != nil || len(mine) != 1 || mine[0].ID != "ci-1" {
		t.Fatalf("list = %+v, %v", mine, err)
	}

	// TouchLastUsed.
	now := t0.Add(time.Hour)
	if err := ts.TouchLastUsed(ctx, "ci-1", now); err != nil {
		t.Fatal(err)
	}
	got, _, _ = ts.Get(ctx, "ci-1")
	if got.LastUsed == nil || !got.LastUsed.Equal(now) {
		t.Fatalf("last_used = %v", got.LastUsed)
	}

	// Delete.
	if err := ts.Delete(ctx, "ci-1"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := ts.Get(ctx, "ci-1"); ok {
		t.Fatal("token survived delete")
	}
}
