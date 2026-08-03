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

func (f *fakeConvergence) RingStatus(_ context.Context, groups []string, _ string) (rollout.RingStatus, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	var sum rollout.RingStatus
	for _, g := range groups {
		rs := f.m[g]
		sum.Total += rs.Total
		sum.OnTarget += rs.OnTarget
		sum.Healthy += rs.Healthy
		sum.Broken += rs.Broken
		sum.Absent += rs.Absent
	}
	return sum, nil
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

// fakeCacheBuilder scripts the release-build phases the rollout polls.
type fakeCacheBuilder struct {
	cancelled int
	phase     ports.BuildPhase
	// calls records every EnsureBuilt invocation for assertions.
	calls []struct {
		Rev   string
		Hosts []string
	}
}

// CancelBuilds records that the rollout asked the runner to stop. Cancelling
// used to change the bookkeeping and leave the work running.
func (f *fakeCacheBuilder) CancelBuilds(context.Context) error { f.cancelled++; return nil }

func (f *fakeCacheBuilder) EnsureBuilt(_ context.Context, rev string, hosts []string) (ports.BuildState, error) {
	f.calls = append(f.calls, struct {
		Rev   string
		Hosts []string
	}{rev, hosts})
	return ports.BuildState{Phase: f.phase, Detail: "scripted"}, nil
}

// Build-before-promote: while the release build runs the promotion is held
// (no pin, no branch move); once the cache reports done the same tick path
// promotes. The builder sees the target revision and the ring's active hosts.
func TestRolloutHoldsPromotionUntilBuilt(t *testing.T) {
	rs, svc, _, _, _ := newRolloutStack(t)
	builder := &fakeCacheBuilder{phase: ports.BuildBuilding}
	rs.WithCacheBuilder(builder)
	ctx := context.Background()

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	// Building: promotion held, no pin committed, build request recorded.
	tick(t, rs)
	if pin := svc.Fleet().Groups["canary"].Pin; pin != "" {
		t.Fatalf("promoted before the release was built (pin %q)", pin)
	}
	st, _, _ := rs.Status(ctx)
	if _, seen := st.BuildRequestedAt[0]; !seen {
		t.Fatal("build request not recorded on the run state")
	}
	if len(builder.calls) == 0 || builder.calls[0].Rev != "rev-2" ||
		len(builder.calls[0].Hosts) != 1 || builder.calls[0].Hosts[0] != "c-1" {
		t.Fatalf("builder saw %+v, want rev-2 for canary's host c-1", builder.calls)
	}

	// Done: the next tick promotes and clears the request marker.
	builder.phase = ports.BuildDone
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatal("want promote once built")
	}
	if pin := svc.Fleet().Groups["canary"].Pin; pin != "rev-2" {
		t.Fatalf("canary pin = %q after built promote", pin)
	}
	st, _, _ = rs.Status(ctx)
	if _, seen := st.BuildRequestedAt[0]; seen {
		t.Fatal("build request marker not cleared on promotion")
	}
}

// A failed release build halts the run: shipping an unbuilt release is what
// build-before-promote exists to prevent.
func TestRolloutHaltsOnFailedBuild(t *testing.T) {
	rs, svc, _, _, _ := newRolloutStack(t)
	rs.WithCacheBuilder(&fakeCacheBuilder{phase: ports.BuildFailed})
	ctx := context.Background()

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	tick(t, rs)
	st, _, _ := rs.Status(ctx)
	if st.Status != rollout.Halted || !contains(st.Reason, "release build failed") {
		t.Fatalf("state = %+v, want halted on failed build", st)
	}
	if pin := svc.Fleet().Groups["canary"].Pin; pin != "" {
		t.Fatalf("failed build still promoted (pin %q)", pin)
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
	conv.set("canary", rollout.RingStatus{Total: 1, OnTarget: 1, Healthy: 0, Broken: 1})
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

// TestRolloutStatusReportsCohortAccounting (finding: Status() omits cohort
// accounting that Tick() sets): a count-capped wave's Status() must report
// Released/GroupTotal scoped to the whole active group, matching what Tick
// computes internally - otherwise an operator reading Status sees e.g. "1/1
// on target" with GroupTotal=0 for a wave that released only 1 of 3 devices.
func TestRolloutStatusReportsCohortAccounting(t *testing.T) {
	rs, _, conv, _ := newCohortStack(t)
	ctx := context.Background()
	if _, err := rs.Start(ctx, "rev-9", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	// Promote: releases the first cohort (1 of 3 devices).
	if k := tick(t, rs); k != rollout.Promote {
		t.Fatalf("tick1 = %s, want promote", k)
	}
	conv.set("wave", rollout.RingStatus{Total: 1, OnTarget: 1, Healthy: 1})

	_, statuses, err := rs.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(statuses) != 1 {
		t.Fatalf("statuses = %v", statuses)
	}
	if statuses[0].Released != 1 || statuses[0].GroupTotal != 3 {
		t.Fatalf("status cohort accounting = %+v, want Released=1 GroupTotal=3", statuses[0])
	}
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

// Cancelling a run must stop the work, not only the bookkeeping. On
// 2026-08-01 a cancelled run left its release build going; it finished the job
// it had been told to abandon and OOM-killed the gate, which stops every path
// that commits configuration.
func TestCancelStopsTheReleaseBuild(t *testing.T) {
	rs, _, _, _, _ := newRolloutStack(t)
	builder := &fakeCacheBuilder{phase: ports.BuildBuilding}
	rs.WithCacheBuilder(builder)

	if _, err := rs.Start(context.Background(), "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Cancel(context.Background()); err != nil {
		t.Fatal(err)
	}
	if builder.cancelled != 1 {
		t.Fatalf("cancel asked the runner to stop %d times, want 1", builder.cancelled)
	}
}
