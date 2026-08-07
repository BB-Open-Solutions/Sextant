package app

import (
	"context"
	"errors"
	"testing"
	"time"
)

// retention_test.go covers the sweeper's decisions. The SQL is proven
// against real Postgres in the adapter; what is proven here is what the
// sweeper asks for and what it refuses to ask for.

type memRetention struct {
	cutoffs map[string]time.Time
	known   map[string]bool
	calls   []string
	fail    map[string]error
}

func newMemRetention() *memRetention {
	return &memRetention{cutoffs: map[string]time.Time{}, fail: map[string]error{}}
}

func (m *memRetention) note(kind string, cutoff time.Time) (int, error) {
	m.calls = append(m.calls, kind)
	m.cutoffs[kind] = cutoff
	if err := m.fail[kind]; err != nil {
		return 0, err
	}
	return 1, nil
}

func (m *memRetention) DeleteNotificationsBefore(_ context.Context, _ string, c time.Time) (int, error) {
	return m.note("notifications", c)
}
func (m *memRetention) DeleteElevationBefore(_ context.Context, _ string, c time.Time) (int, error) {
	return m.note("elevation", c)
}
func (m *memRetention) DeleteSeenUsersBefore(_ context.Context, _ string, c time.Time) (int, error) {
	return m.note("seenUsers", c)
}
func (m *memRetention) DeleteDeviceStatusBefore(_ context.Context, _ string, c time.Time, known map[string]bool) (int, error) {
	m.known = known
	return m.note("deviceStatus", c)
}

func TestSweepUsesEachWindow(t *testing.T) {
	store := newMemRetention()
	now := time.Date(2026, 8, 7, 12, 0, 0, 0, time.UTC)
	pol := RetentionPolicy{
		Notifications: 10 * 24 * time.Hour,
		Elevation:     20 * 24 * time.Hour,
		SeenUsers:     30 * 24 * time.Hour,
		DeviceStatus:  40 * 24 * time.Hour,
	}
	s := NewRetentionSweeper(store, pol, "t1", clockAt{now}, nil).
		WithFleet(func() map[string]bool { return map[string]bool{"lt-1": true} })

	res, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Total() != 4 {
		t.Errorf("total removed = %d, want 4", res.Total())
	}
	for kind, d := range map[string]time.Duration{
		"notifications": pol.Notifications, "elevation": pol.Elevation,
		"seenUsers": pol.SeenUsers, "deviceStatus": pol.DeviceStatus,
	} {
		want := now.Add(-d)
		if got := store.cutoffs[kind]; !got.Equal(want) {
			t.Errorf("%s cutoff = %v, want %v - the wrong window deletes the wrong data", kind, got, want)
		}
	}
	if !store.known["lt-1"] {
		t.Error("the live fleet was not passed through; a real device could be swept")
	}
}

// TestAZeroWindowDisablesThatSweep: a deployment that has not decided on a
// retention period must not have one chosen for it silently.
func TestAZeroWindowDisablesThatSweep(t *testing.T) {
	store := newMemRetention()
	s := NewRetentionSweeper(store, RetentionPolicy{Elevation: time.Hour},
		"t1", clockAt{time.Now()}, nil).
		WithFleet(func() map[string]bool { return map[string]bool{"lt-1": true} })
	if _, err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(store.calls) != 1 || store.calls[0] != "elevation" {
		t.Errorf("swept %v; only the configured window should run", store.calls)
	}
}

// TestSweepRefusesAnEmptyFleet mirrors the credential sweep's refusal. With
// no known tags every check-in looks orphaned, so a fleet document that
// failed to load would erase the observed plane.
func TestSweepRefusesAnEmptyFleet(t *testing.T) {
	store := newMemRetention()
	s := NewRetentionSweeper(store, DefaultRetention(), "t1", clockAt{time.Now()}, nil).
		WithFleet(func() map[string]bool { return map[string]bool{} })
	res, err := s.Sweep(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range store.calls {
		if c == "deviceStatus" {
			t.Fatal("device status was swept against an empty fleet document")
		}
	}
	if res.DeviceStatus != 0 {
		t.Errorf("reported %d device rows removed", res.DeviceStatus)
	}
	// The other kinds are unaffected: they do not depend on the fleet.
	if res.Notifications == 0 {
		t.Error("an empty fleet stopped the sweeps that have nothing to do with it")
	}
}

// TestSweepWithNoFleetSourceSkipsDeviceStatus: without a way to tell which
// devices exist, deleting check-in history would erase the evidence that a
// device is silent - which is the only thing that record is for.
func TestSweepWithNoFleetSourceSkipsDeviceStatus(t *testing.T) {
	store := newMemRetention()
	s := NewRetentionSweeper(store, DefaultRetention(), "t1", clockAt{time.Now()}, nil)
	if _, err := s.Sweep(context.Background()); err != nil {
		t.Fatal(err)
	}
	for _, c := range store.calls {
		if c == "deviceStatus" {
			t.Fatal("device status was swept with no fleet source at all")
		}
	}
}

// TestOneFailureDoesNotStopTheOthers: three categories must not keep growing
// because of a problem in the fourth.
func TestOneFailureDoesNotStopTheOthers(t *testing.T) {
	store := newMemRetention()
	store.fail["notifications"] = errors.New("table locked")
	s := NewRetentionSweeper(store, DefaultRetention(), "t1", clockAt{time.Now()}, nil).
		WithFleet(func() map[string]bool { return map[string]bool{"lt-1": true} })

	res, err := s.Sweep(context.Background())
	if err == nil {
		t.Error("a failing sweep reported success; nobody would learn it is not running")
	}
	if len(store.calls) != 4 {
		t.Errorf("only %v ran; one failure stopped the rest", store.calls)
	}
	if res.Elevation == 0 || res.SeenUsers == 0 {
		t.Errorf("the other kinds did not sweep: %+v", res)
	}
}

// TestDefaultsAreGenerousAndNonZero: the defaults are a fallback for a
// deployment that has not decided, and the safe direction is long. A zero
// here would silently disable the whole feature.
func TestDefaultsAreGenerousAndNonZero(t *testing.T) {
	d := DefaultRetention()
	for name, got := range map[string]time.Duration{
		"notifications": d.Notifications, "elevation": d.Elevation,
		"seenUsers": d.SeenUsers, "deviceStatus": d.DeviceStatus,
	} {
		if got <= 0 {
			t.Errorf("%s default is %v, which disables the sweep", name, got)
		}
		if got < 90*24*time.Hour {
			t.Errorf("%s default is %v - under three months is too aggressive for data nobody asked us to delete", name, got)
		}
	}
}
