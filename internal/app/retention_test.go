package app

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"sync"
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

// countingRetention is memRetention with a signal, so a test can wait for
// sweeps to happen instead of sleeping and hoping.
type countingRetention struct {
	*memRetention
	mu     sync.Mutex
	sweeps int
	tick   chan struct{}
}

func newCountingRetention() *countingRetention {
	return &countingRetention{memRetention: newMemRetention(), tick: make(chan struct{}, 64)}
}

func (c *countingRetention) DeleteNotificationsBefore(ctx context.Context, t string, cu time.Time) (int, error) {
	c.mu.Lock()
	c.sweeps++
	c.mu.Unlock()
	select {
	case c.tick <- struct{}{}:
	default:
	}
	return c.memRetention.DeleteNotificationsBefore(ctx, t, cu)
}

func (c *countingRetention) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.sweeps
}

// Run is the loop that makes retention a promise rather than a one-off. The
// property worth pinning is not that it sweeps, it is that it KEEPS
// sweeping: a sweep that returns an error must not end the loop.
//
// If it did, one transient database error would stop deletion for the life
// of the process, with no symptom anywhere. The processing register says
// records are removed after their window, and that sentence would quietly
// stop being true.
func TestRunKeepsSweepingAfterAFailure(t *testing.T) {
	store := newCountingRetention()
	store.fail["notifications"] = errors.New("database is having a moment")

	s := NewRetentionSweeper(store, DefaultRetention(), "t1",
		clockAt{time.Now()}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx, time.Millisecond) }()

	// Three sweeps, every one of them failing. Waiting on the signal rather
	// than on the clock: a sleep long enough to be reliable is a slow test,
	// and one short enough to be fast is a flaky one.
	for i := range 3 {
		select {
		case <-store.tick:
		case <-time.After(5 * time.Second):
			t.Fatalf("the loop stopped after %d sweeps; a failing sweep ended it", i)
		}
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return on a cancelled context")
	}
	if n := store.count(); n < 3 {
		t.Errorf("only %d sweeps", n)
	}
}

// The first sweep waits one interval. Worth stating rather than discovering:
// a deployment that restarts often would sweep on every boot if it did not,
// and the windows here are months.
//
// The first version of this test started the loop, cancelled it and read a
// counter, which observed nothing at all: the goroutine need not have run
// yet, and an added immediate sweep survived it. Waiting a bounded time for
// a sweep SIGNAL is the version that can fail, because an immediate sweep
// arrives in microseconds and a one-hour tick does not arrive at all.
func TestRunDoesNotSweepBeforeTheFirstTick(t *testing.T) {
	store := newCountingRetention()
	s := NewRetentionSweeper(store, DefaultRetention(), "t1",
		clockAt{time.Now()}, slog.New(slog.NewTextHandler(io.Discard, nil)))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go s.Run(ctx, time.Hour)

	select {
	case <-store.tick:
		t.Fatal("a sweep happened before the first tick; with an hour between " +
			"ticks that can only be a sweep at startup")
	case <-time.After(250 * time.Millisecond):
	}
}
