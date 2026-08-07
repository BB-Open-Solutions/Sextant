package app

import (
	"context"
	"testing"
	"time"
)

// TestBoundCredentialsStopWorkingWhenTheyExpire closes a gap found by
// mutation on 2026-08-07: removing the expiry check from authenticateBound
// broke NO test. Revocation was covered, the wrong tag was covered, the
// wrong kind was covered - expiry was not, on either credential type.
//
// It matters more than the five-year TTL suggests. boundCredTTL is long
// precisely because these credentials are meant to be revoked rather than
// aged out, which makes expiry the quiet path nobody exercises: if it broke,
// the first evidence would be a credential from a decommissioned machine
// still working in 2031.
func TestBoundCredentialsStopWorkingWhenTheyExpire(t *testing.T) {
	ctx := context.Background()

	t.Run("device", func(t *testing.T) {
		store := newMemTokenStore()
		clock := newFakeClock(testT0)
		dc := NewDeviceCredentials(store, clock)

		secret, err := dc.Issue(ctx, "lt-1")
		if err != nil {
			t.Fatal(err)
		}
		if !dc.AuthenticateTag(ctx, secret, "lt-1") {
			t.Fatal("precondition: a fresh credential does not authenticate")
		}
		// One second before the TTL it still works...
		clock.Advance(boundCredTTL - time.Second)
		if !dc.AuthenticateTag(ctx, secret, "lt-1") {
			t.Error("the credential expired early")
		}
		// ...and past it, it does not.
		clock.Advance(2 * time.Second)
		if dc.AuthenticateTag(ctx, secret, "lt-1") {
			t.Error("an expired device credential still authenticates")
		}
		if _, ok := dc.Authenticate(ctx, secret); ok {
			t.Error("an expired credential still resolves to a tag")
		}
	})

	t.Run("station", func(t *testing.T) {
		store := newMemTokenStore()
		clock := newFakeClock(testT0)
		sc := NewStationCredentials(store, clock)

		secret, err := sc.Issue(ctx, "nuc-1")
		if err != nil {
			t.Fatal(err)
		}
		if !sc.AuthenticateTag(ctx, secret, "nuc-1") {
			t.Fatal("precondition: a fresh station credential does not authenticate")
		}
		clock.Advance(boundCredTTL + time.Second)
		if sc.AuthenticateTag(ctx, secret, "nuc-1") {
			t.Error("an expired station credential still authenticates")
		}
	})

	t.Run("an expired credential is not 'has one' for the bridge", func(t *testing.T) {
		// HasCredential deliberately answers true for an expired credential:
		// the remedy is to re-issue it, not to let the shared bridge token
		// take over for that device. Pinned here because it reads like a bug
		// and is not.
		store := newMemTokenStore()
		clock := newFakeClock(testT0)
		dc := NewDeviceCredentials(store, clock)
		if _, err := dc.Issue(ctx, "lt-1"); err != nil {
			t.Fatal(err)
		}
		clock.Advance(boundCredTTL + time.Hour)
		has, err := dc.HasCredential(ctx, "lt-1")
		if err != nil {
			t.Fatal(err)
		}
		if !has {
			t.Error("an expired credential reads as absent; the bridge token would speak for this device")
		}
	})
}
