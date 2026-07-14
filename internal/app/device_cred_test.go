package app

import (
	"context"
	"testing"
	"time"
)

func TestDeviceCredentialsIssueAndAuthenticate(t *testing.T) {
	store := newMemTokenStore()
	dc := NewDeviceCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	secretA, err := dc.Issue(ctx, "lt-1")
	if err != nil {
		t.Fatal(err)
	}
	secretB, err := dc.Issue(ctx, "lt-2")
	if err != nil {
		t.Fatal(err)
	}

	// Each credential proves exactly its own tag.
	if tag, ok := dc.Authenticate(ctx, secretA); !ok || tag != "lt-1" {
		t.Fatalf("A auth = %q %v", tag, ok)
	}
	if tag, ok := dc.Authenticate(ctx, secretB); !ok || tag != "lt-2" {
		t.Fatalf("B auth = %q %v", tag, ok)
	}

	// THE IMPERSONATION GAP: lt-1's credential cannot check in as lt-2.
	if dc.AuthenticateTag(ctx, secretA, "lt-2") {
		t.Fatal("device A impersonated device B")
	}
	if !dc.AuthenticateTag(ctx, secretA, "lt-1") {
		t.Fatal("device A rejected for its own tag")
	}

	// Garbage and cross-secret rejected.
	if _, ok := dc.Authenticate(ctx, "not-a-token"); ok {
		t.Error("garbage accepted")
	}
	if dc.AuthenticateTag(ctx, secretA+"x", "lt-1") {
		t.Error("tampered secret accepted")
	}
}

func TestDeviceCredentialReissueRotates(t *testing.T) {
	store := newMemTokenStore()
	dc := NewDeviceCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	old, _ := dc.Issue(ctx, "lt-1")
	fresh, _ := dc.Issue(ctx, "lt-1") // re-image
	if old == fresh {
		t.Fatal("re-issue produced the same secret")
	}
	// The old credential no longer works; the fresh one does.
	if _, ok := dc.Authenticate(ctx, old); ok {
		t.Error("old credential still valid after re-issue")
	}
	if tag, ok := dc.Authenticate(ctx, fresh); !ok || tag != "lt-1" {
		t.Errorf("fresh credential = %q %v", tag, ok)
	}
}

func TestDeviceCredentialRevoke(t *testing.T) {
	store := newMemTokenStore()
	dc := NewDeviceCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	secret, _ := dc.Issue(ctx, "lt-1")
	if err := dc.Revoke(ctx, "lt-1"); err != nil {
		t.Fatal(err)
	}
	if dc.AuthenticateTag(ctx, secret, "lt-1") {
		t.Fatal("revoked device credential still authenticates")
	}
}

func TestDeviceCredentialNotAPersonalToken(t *testing.T) {
	// A device credential and a personal token share the store; a device
	// credential must not authenticate the operator API, nor a personal
	// token the check-in.
	store := newMemTokenStore()
	dc := NewDeviceCredentials(store, newFakeClock(testT0))
	ts := NewTokenService(store, newFakeClock(testT0), time.Hour)
	ctx := context.Background()

	devSecret, _ := dc.Issue(ctx, "lt-1")
	_, personalSecret, _ := ts.Mint(ctx, MintRequest{
		ID: "ada", Name: "Ada", Kind: "personal", Subject: "sub-ada"})

	// A personal token is not a device credential.
	if _, ok := dc.Authenticate(ctx, personalSecret); ok {
		t.Error("personal token authenticated as a device")
	}
	// A device credential must NOT authenticate the operator API at all -
	// wrong path, rejected outright (defense in depth beyond "no groups").
	if _, _, ok := ts.Authenticate(ctx, devSecret); ok {
		t.Error("device credential authenticated the operator API")
	}
}

func TestStationCredentialNotAnOperatorToken(t *testing.T) {
	// A station credential is a real token record (valid hash, long TTL); it
	// may only submit discoveries and must never authenticate the operator API,
	// even before the group-based authz layer would refuse it.
	store := newMemTokenStore()
	ts := NewTokenService(store, newFakeClock(testT0), time.Hour)
	ctx := context.Background()

	_, stationSecret, err := ts.Mint(ctx, MintRequest{
		ID: "station-nuc-1", Name: "NUC 1", Kind: "station", Subject: "station-nuc-1"})
	if err != nil {
		t.Fatalf("mint station credential: %v", err)
	}
	if _, _, ok := ts.Authenticate(ctx, stationSecret); ok {
		t.Error("station credential authenticated the operator API")
	}
}
