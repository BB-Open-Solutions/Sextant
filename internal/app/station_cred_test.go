package app

import (
	"context"
	"testing"
)

func TestStationCredentialsBindToStation(t *testing.T) {
	store := newMemTokenStore()
	sc := NewStationCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	secretA, err := sc.Issue(ctx, "nuc-1")
	if err != nil {
		t.Fatal(err)
	}
	secretB, err := sc.Issue(ctx, "nuc-2")
	if err != nil {
		t.Fatal(err)
	}

	// A station credential proves exactly its own station tag.
	if !sc.AuthenticateTag(ctx, secretA, "nuc-1") {
		t.Fatal("station A rejected for its own tag")
	}
	// THE IMPERSONATION GAP: nuc-1's credential cannot report as nuc-2.
	if sc.AuthenticateTag(ctx, secretA, "nuc-2") {
		t.Fatal("station A reported as station B")
	}
	if sc.AuthenticateTag(ctx, secretB, "nuc-1") {
		t.Fatal("station B reported as station A")
	}
	if sc.AuthenticateTag(ctx, "not-a-token", "nuc-1") {
		t.Fatal("garbage accepted")
	}
	if sc.AuthenticateTag(ctx, secretA+"x", "nuc-1") {
		t.Fatal("tampered secret accepted")
	}
}

func TestStationCredentialReissueRotatesAndRevoke(t *testing.T) {
	store := newMemTokenStore()
	sc := NewStationCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	old, _ := sc.Issue(ctx, "nuc-1")
	fresh, _ := sc.Issue(ctx, "nuc-1")
	if old == fresh {
		t.Fatal("re-issue produced the same secret")
	}
	if sc.AuthenticateTag(ctx, old, "nuc-1") {
		t.Fatal("old station credential still valid after re-issue")
	}
	if !sc.AuthenticateTag(ctx, fresh, "nuc-1") {
		t.Fatal("fresh station credential rejected")
	}
	if err := sc.Revoke(ctx, "nuc-1"); err != nil {
		t.Fatal(err)
	}
	if sc.AuthenticateTag(ctx, fresh, "nuc-1") {
		t.Fatal("revoked station credential still authenticates")
	}
}

func TestStationAndDeviceCredentialsDoNotCross(t *testing.T) {
	// Station and device credentials share the token store. The kind wall
	// must hold both ways: a device credential must not pass as a station
	// report, nor a station credential as a device check-in - even when the
	// tag string happens to match.
	store := newMemTokenStore()
	sc := NewStationCredentials(store, newFakeClock(testT0))
	dc := NewDeviceCredentials(store, newFakeClock(testT0))
	ctx := context.Background()

	stationSecret, _ := sc.Issue(ctx, "shared-tag")
	deviceSecret, _ := dc.Issue(ctx, "shared-tag")

	if dc.AuthenticateTag(ctx, stationSecret, "shared-tag") {
		t.Error("station credential authenticated a device check-in")
	}
	if sc.AuthenticateTag(ctx, deviceSecret, "shared-tag") {
		t.Error("device credential authenticated a station report")
	}
	// Each still works on its own path.
	if !sc.AuthenticateTag(ctx, stationSecret, "shared-tag") {
		t.Error("station credential rejected on its own path")
	}
	if !dc.AuthenticateTag(ctx, deviceSecret, "shared-tag") {
		t.Error("device credential rejected on its own path")
	}
}
