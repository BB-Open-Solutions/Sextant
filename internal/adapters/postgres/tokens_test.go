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
	other, _, err := token.Mint("ci-2", "n", token.Personal, "sub-bob", nil, "", t0, time.Hour)
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
	if err := ts.Put(ctx, other); err != nil {
		t.Fatalf("setup: %v", err)
	}
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

// TestListByKindSeparatesTheFleetFromThePeople covers the inventory the
// console renders per token kind.
//
// The kinds are not labels. A device credential and a person's personal
// token authorise different things, and the page that lists them is where an
// operator goes to revoke one. A query that mixes them either hides a
// credential that should be revoked, or shows one that cannot be.
func TestListByKindSeparatesTheFleetFromThePeople(t *testing.T) {
	s := openStore(t)
	ts := s.Tokens()
	ctx := context.Background()

	seed := func(id string, kind token.Kind, subject string, created time.Time) {
		t.Helper()
		tok, _, err := token.Mint(id, id, kind, subject, nil, "viewer", created, time.Hour)
		if err != nil {
			t.Fatal(err)
		}
		if err := ts.Put(ctx, tok); err != nil {
			t.Fatal(err)
		}
	}
	seed("dev-old", token.Device, "nuc-01", t0.Add(-2*time.Hour))
	seed("dev-new", token.Device, "nuc-02", t0)
	seed("person", token.Personal, "sub-ada", t0.Add(-time.Hour))
	seed("station", token.Station, "station-01", t0.Add(-time.Hour))

	devices, err := ts.ListByKind(ctx, token.Device)
	if err != nil {
		t.Fatalf("list devices: %v", err)
	}
	if len(devices) != 2 {
		t.Fatalf("device tokens = %d, want 2: %+v", len(devices), devices)
	}
	// Newest first: the list is read top-down when hunting a credential that
	// was just issued.
	if devices[0].ID != "dev-new" || devices[1].ID != "dev-old" {
		t.Errorf("order = %s, %s; want newest first", devices[0].ID, devices[1].ID)
	}
	for _, d := range devices {
		if d.Kind != token.Device {
			t.Errorf("token %s has kind %q in the device list", d.ID, d.Kind)
		}
		// The hash has to survive the read, or revoking from this page would
		// act on a row that no longer matches the credential in the field.
		if d.Hash == "" {
			t.Errorf("token %s came back without its hash", d.ID)
		}
	}

	// Each remaining kind answers for itself, and an unused kind answers
	// empty rather than falling back to everything.
	for kind, want := range map[token.Kind]int{
		token.Personal: 1,
		token.Station:  1,
		token.Service:  0,
	} {
		got, err := ts.ListByKind(ctx, kind)
		if err != nil {
			t.Fatalf("list %s: %v", kind, err)
		}
		if len(got) != want {
			t.Errorf("%s tokens = %d, want %d: %+v", kind, len(got), want, got)
		}
	}
}
