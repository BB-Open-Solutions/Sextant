package app

import (
	"context"
	"testing"
)

// TestReconcileRevokesCredentialsWithNoDevice: measured in production on
// 2026-08-06, 15 live device credentials against 2 devices in the fleet. Both
// removal paths revoke as they go, so the orphans are not a forgotten call -
// they are what happens when a device leaves fleet.json any other way, because
// the credential's lifecycle hangs off two handlers rather than off the
// document.
func TestReconcileRevokesCredentialsWithNoDevice(t *testing.T) {
	ctx := context.Background()
	store := newMemTokenStore()
	d := NewDeviceCredentials(store, newFakeClock(testT0))

	secrets := map[string]string{}
	for _, tag := range []string{"lt-1", "lt-2", "ghost-a", "ghost-b"} {
		s, err := d.Issue(ctx, tag)
		if err != nil {
			t.Fatal(err)
		}
		secrets[tag] = s
	}
	// A credential of a different kind must be left alone: the station's
	// credential lives in the same store and is not a device credential.
	if _, err := NewStationCredentials(store, newFakeClock(testT0)).Issue(ctx, "nuc-1"); err != nil {
		t.Fatal(err)
	}

	n, err := d.ReconcileWithFleet(ctx, map[string]bool{"lt-1": true, "lt-2": true})
	if err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("revoked %d, want the two orphans", n)
	}
	// Assert the surviving credentials still AUTHENTICATE, not merely that a
	// row is present: the sweep must leave a working credential behind, and a
	// test that only counts rows would pass on a corrupted one.
	for _, tag := range []string{"lt-1", "lt-2"} {
		if !d.AuthenticateTag(ctx, secrets[tag], tag) {
			t.Errorf("%s is in the fleet and its credential no longer authenticates", tag)
		}
	}
	for _, tag := range []string{"ghost-a", "ghost-b"} {
		if d.AuthenticateTag(ctx, secrets[tag], tag) {
			t.Errorf("%s has no device and its credential still authenticates", tag)
		}
	}
	for _, tag := range []string{"ghost-a", "ghost-b"} {
		if _, ok, _ := store.Get(ctx, "device-"+tag); ok {
			t.Errorf("%s has no device and kept its credential", tag)
		}
	}
	if _, ok, _ := store.Get(ctx, "station-nuc-1"); !ok {
		t.Error("the station credential was swept; only device credentials are in scope")
	}
}

// TestReconcileRefusesAnEmptyFleet is the guard that matters most. A document
// that failed to load, or a restore that has not finished, would otherwise
// revoke every credential in the fleet. Leaving orphans is a small problem;
// locking out every device is a large one.
func TestReconcileRefusesAnEmptyFleet(t *testing.T) {
	ctx := context.Background()
	store := newMemTokenStore()
	d := NewDeviceCredentials(store, newFakeClock(testT0))
	if _, err := d.Issue(ctx, "lt-1"); err != nil {
		t.Fatal(err)
	}

	n, err := d.ReconcileWithFleet(ctx, map[string]bool{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("revoked %d against an empty document", n)
	}
	if _, ok, _ := store.Get(ctx, "device-lt-1"); !ok {
		t.Fatal("an empty fleet document locked a real device out")
	}
}

// TestReconcileKeepsRetiredDevices: a retired device stays in the document and
// can be reactivated, so its credential is not an orphan. Reactivate re-issues
// anyway, but sweeping it here would revoke a credential the fleet still knows
// about - and that is the class of mistake this whole sweep exists to prevent.
func TestReconcileKeepsRetiredDevices(t *testing.T) {
	ctx := context.Background()
	store := newMemTokenStore()
	d := NewDeviceCredentials(store, newFakeClock(testT0))
	if _, err := d.Issue(ctx, "parked"); err != nil {
		t.Fatal(err)
	}

	// deviceTags (cmd/sextant) includes retired devices; this mirrors that.
	if _, err := d.ReconcileWithFleet(ctx, map[string]bool{"parked": true}); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := store.Get(ctx, "device-parked"); !ok {
		t.Fatal("a retired device lost its credential")
	}
}
