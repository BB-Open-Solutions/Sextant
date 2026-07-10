package postgres

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

func TestPrefsRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	now := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)

	// Missing prefs read as not-found, no error.
	if _, ok, err := s.GetPrefs(ctx, "t1", "ada"); ok || err != nil {
		t.Fatalf("empty get = %v, %v", ok, err)
	}

	p := identity.Preferences{Timezone: "Europe/Amsterdam", Locale: "nl"}
	if err := s.PutPrefs(ctx, "t1", "ada", p, now); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetPrefs(ctx, "t1", "ada")
	if err != nil || !ok || got != p {
		t.Fatalf("get = %+v, %v, %v", got, ok, err)
	}

	// Upsert replaces.
	p2 := identity.Preferences{Timezone: "UTC", Locale: "en"}
	if err := s.PutPrefs(ctx, "t1", "ada", p2, now.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got, _, _ := s.GetPrefs(ctx, "t1", "ada"); got != p2 {
		t.Fatalf("after upsert = %+v", got)
	}

	// Tenant isolation.
	if _, ok, _ := s.GetPrefs(ctx, "t2", "ada"); ok {
		t.Fatal("prefs leaked across tenants")
	}
}
