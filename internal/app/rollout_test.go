package app

import (
	"context"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// fakeConvergence scripts per-group ring status.
type fakeConvergence struct {
	mu sync.Mutex
	m  map[string]rollout.RingStatus
}

func (f *fakeConvergence) set(group string, rs rollout.RingStatus) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.m == nil {
		f.m = map[string]rollout.RingStatus{}
	}
	f.m[group] = rs
}

func (f *fakeConvergence) RingStatus(_ context.Context, group, _ string) (rollout.RingStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.m[group], nil
}

const rolloutFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"canary": {}, "fleet": {}},
  "devices": {
    "c-1": {"groups": ["canary"], "hardware": "hw"},
    "f-1": {"groups": ["fleet"], "hardware": "hw"},
    "f-2": {"groups": ["fleet"], "hardware": "hw"}
  },
  "rollout": {"rings": [
    {"group": "canary", "soakMinutes": 60},
    {"group": "fleet"}
  ]}
}`

// newRolloutStack builds the rollout stack over a real git repo.
func newRolloutStack(t *testing.T) (*RolloutService, *ConfigService, *fakeConvergence, *fakeClock, string) {
	t.Helper()
	dir := t.TempDir()
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(rolloutFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	shr("add", "fleet.json")
	shr("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conv := &fakeConvergence{}
	clock := newFakeClock(testT0)
	rs := NewRolloutService(svc, st.Rollouts(), conv, clock,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return rs, svc, conv, clock, dir
}

// tick asserts one Tick and returns the action kind.
func tick(t *testing.T, rs *RolloutService) rollout.ActionKind {
	t.Helper()
	act, _, err := rs.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if act == nil {
		return ""
	}
	return act.Kind
}

func TestRolloutFullRun(t *testing.T) {
	rs, svc, conv, clock, dir := newRolloutStack(t)
	ctx := context.Background()

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	// Second start refused while active.
	if _, err := rs.Start(ctx, "rev-3", ports.Author{}); err == nil {
		t.Fatal("second active rollout accepted")
	}

	// Tick 1: promote canary -> pin committed via the gated transaction.
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatalf("tick1 = %s, want promote", k)
	}
	if pin := svc.Fleet().Groups["canary"].Pin; pin != "rev-2" {
		t.Fatalf("canary pin = %q", pin)
	}
	if log := sh(t, dir, "log", "-1", "--format=%an %s"); !contains(log, "rollout") {
		t.Fatalf("pin not committed with rollout attribution: %s", log)
	}

	// Tick 2: canary converging.
	conv.set("canary", rollout.RingStatus{Total: 1, OnTarget: 0, Healthy: 0})
	if k := tick(t, rs); k != rollout.Wait {
		t.Fatal("want wait while converging")
	}

	// Tick 3: converged -> soak starts.
	conv.set("canary", rollout.RingStatus{Total: 1, OnTarget: 1, Healthy: 1})
	if k := tick(t, rs); k != rollout.Wait {
		t.Fatal("want wait starting soak")
	}
	// Tick 4: mid-soak.
	clock.Advance(30 * time.Minute)
	if k := tick(t, rs); k != rollout.Wait {
		t.Fatal("want wait mid-soak")
	}
	// Tick 5: soak elapsed -> advance to fleet ring.
	clock.Advance(31 * time.Minute)
	if k := tick(t, rs); k != rollout.Advance {
		t.Fatal("want advance after soak")
	}
	// Tick 6: promote fleet.
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatal("want promote fleet")
	}
	if pin := svc.Fleet().Groups["fleet"].Pin; pin != "rev-2" {
		t.Fatalf("fleet pin = %q", pin)
	}
	// Tick 7: fleet converged (soak 0, last ring) -> done... needs the
	// convergence recorded first (wait), then done.
	conv.set("fleet", rollout.RingStatus{Total: 2, OnTarget: 2, Healthy: 2})
	if k := tick(t, rs); k != rollout.Wait {
		t.Fatal("want wait recording convergence")
	}
	if k := tick(t, rs); k != rollout.Done {
		t.Fatal("want done on last ring")
	}

	st, _, err := rs.Status(ctx)
	if err != nil || st.Status != rollout.Completed {
		t.Fatalf("final status = %+v, %v", st, err)
	}
	// A new rollout may start now.
	if _, err := rs.Start(ctx, "rev-3", ports.Author{}); err != nil {
		t.Fatal(err)
	}
}

func TestRolloutHaltsOnUnhealthyRing(t *testing.T) {
	rs, _, conv, _, _ := newRolloutStack(t)
	ctx := context.Background()

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatal("want promote")
	}
	// Canary device converged but unhealthy: default gate is 100%.
	conv.set("canary", rollout.RingStatus{Total: 1, OnTarget: 1, Healthy: 0})
	if k := tick(t, rs); k != rollout.Halt {
		t.Fatal("want halt")
	}
	st, _, _ := rs.Status(ctx)
	if st.Status != rollout.Halted || st.Reason == "" {
		t.Fatalf("state = %+v", st)
	}
	// Halted run does not tick further.
	act, _, err := rs.Tick(ctx)
	if err != nil || act != nil {
		t.Fatalf("tick on halted = %+v, %v", act, err)
	}
}

func TestRolloutCancel(t *testing.T) {
	rs, _, _, _, _ := newRolloutStack(t)
	ctx := context.Background()
	if _, err := rs.Cancel(ctx); err == nil {
		t.Fatal("cancel without run accepted")
	}
	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	st, err := rs.Cancel(ctx)
	if err != nil || st.Status != rollout.Cancelled {
		t.Fatalf("cancel = %+v, %v", st, err)
	}
}

func TestRolloutStartValidation(t *testing.T) {
	rs, svc, _, _, _ := newRolloutStack(t)
	ctx := context.Background()
	if _, err := rs.Start(ctx, "", ports.Author{}); err == nil {
		t.Fatal("empty target accepted")
	}
	// Break the plan: ring names unknown group.
	if err := svc.Apply(ctx, func(f *fleet.Fleet) error {
		f.Rollout.Rings[0].Group = "ghost"
		return nil
	}, "break plan", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err == nil {
		t.Fatal("plan with unknown group accepted")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }

const cohortFleet = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"wave": {}},
  "devices": {
    "w-1": {"groups": ["wave"], "hardware": "hw"},
    "w-2": {"groups": ["wave"], "hardware": "hw"},
    "w-3": {"groups": ["wave"], "hardware": "hw"}
  },
  "rollout": {"rings": [{"group": "wave", "soakMinutes": 0, "maxDevices": 1}]}
}`

func newCohortStack(t *testing.T) (*RolloutService, *ConfigService, *fakeConvergence, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(cohortFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	shr("add", "fleet.json")
	shr("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	conv := &fakeConvergence{}
	clock := newFakeClock(testT0)
	rs := NewRolloutService(svc, st.Rollouts(), conv, clock,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	return rs, svc, conv, clock
}

func TestRolloutCappedCohort(t *testing.T) {
	rs, svc, conv, clock := newCohortStack(t)
	ctx := context.Background()
	if _, err := rs.Start(ctx, "rev-9", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	pinned := func() []string {
		var out []string
		for _, tag := range []string{"w-1", "w-2", "w-3"} {
			if svc.Fleet().Devices[tag].Pin == "wave" {
				out = append(out, tag)
			}
		}
		return out
	}

	// Tick 1: promote -> release the first cohort (1 device, sorted: w-1).
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatalf("tick1 = %s, want promote", k)
	}
	if p := pinned(); len(p) != 1 || p[0] != "w-1" {
		t.Fatalf("after promote pinned = %v, want [w-1]", p)
	}

	// Cohort converges healthy; soak (0) then widen to release w-2.
	conv.set("wave", rollout.RingStatus{Total: 1, OnTarget: 1, Healthy: 1})
	tick(t, rs)                // Wait: records ConvergedAt
	clock.Advance(time.Minute) // past the 0 soak
	if k := tick(t, rs); k != rollout.WidenCohort {
		t.Fatalf("widen1 = %s, want widen-cohort", k)
	}
	if p := pinned(); len(p) != 2 {
		t.Fatalf("after widen1 pinned = %v, want 2", p)
	}

	// Second cohort converges; widen to release w-3 (the last).
	conv.set("wave", rollout.RingStatus{Total: 2, OnTarget: 2, Healthy: 2})
	tick(t, rs)
	clock.Advance(time.Minute)
	if k := tick(t, rs); k != rollout.WidenCohort {
		t.Fatalf("widen2 = %s, want widen-cohort", k)
	}
	if len(pinned()) != 3 {
		t.Fatalf("after widen2 not all released: %v", pinned())
	}

	// Whole group released and converged -> the run is done.
	conv.set("wave", rollout.RingStatus{Total: 3, OnTarget: 3, Healthy: 3})
	tick(t, rs)
	clock.Advance(time.Minute)
	if k := tick(t, rs); k != rollout.Done {
		t.Fatalf("final = %s, want done", k)
	}
}
