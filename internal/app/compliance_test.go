package app

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// listStatus is an in-memory StatusStore that serves List - the only read the
// compliance view makes.
type listStatus struct {
	m map[string]observed.DeviceStatus
}

func (s *listStatus) Upsert(context.Context, string, observed.CheckIn, time.Time) (bool, error) {
	return false, nil
}

func (s *listStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	st, ok := s.m[tag]
	return st, ok, nil
}

func (s *listStatus) List(context.Context, string) ([]observed.DeviceStatus, error) {
	out := make([]observed.DeviceStatus, 0, len(s.m))
	for tag, st := range s.m {
		st.Tag = tag
		out = append(out, st)
	}
	return out, nil
}
func (s *listStatus) Ping(context.Context) error { return nil }

// newComplianceStack builds the compliance view over a real git repo (so the
// release lookup and HEAD behave as in production) and a rollout store.
func newComplianceStack(t *testing.T, status *listStatus) (*ComplianceService, ports.RolloutStore, *fakeClock) {
	t.Helper()
	return newComplianceStackWith(t, status, rolloutFleet)
}

// newComplianceStackWith is the same over a caller-supplied fleet, for tests
// that need policies or assignments the shared rollout fixture does not carry.
func newComplianceStackWith(t *testing.T, status *listStatus, fleetJSON string) (*ComplianceService, ports.RolloutStore, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(fleetJSON), 0o644); err != nil {
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
	clock := newFakeClock(testT0)
	inv := NewInventoryService(status, nopFacts{}, clock, DefaultTenant)
	return NewComplianceService(svc, inv, clock).WithRollout(st.Rollouts()), st.Rollouts(), clock
}

// stalledIncident returns the fleet-level stall incident, or nil.
func stalledIncident(t *testing.T, cs *ComplianceService) *incident.Incident {
	t.Helper()
	all, err := cs.Incidents(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for i := range all {
		if all[i].Kind == incident.RolloutStalled {
			return &all[i]
		}
	}
	return nil
}

// TestComplianceStalledRun is the live 2026-07-29 finding: a wave promoted to
// devices that can never converge must stop being a silent Wait and become an
// action item that names who is stuck.
func TestComplianceStalledRun(t *testing.T) {
	status := &listStatus{m: map[string]observed.DeviceStatus{
		// c-1 is the canary and never reaches the target.
		"c-1": {Revision: "rev-old", LastSeen: testT0},
	}}
	cs, store, clock := newComplianceStack(t, status)
	ctx := context.Background()

	if in := stalledIncident(t, cs); in != nil {
		t.Fatalf("no run in flight but a stall was raised: %+v", in)
	}

	run := rollout.NewState("rev-target-abcdef01", testT0)
	run.Rings = []rollout.Ring{{Group: "canary", Name: "Canary"}, {Group: "fleet"}}
	run.PromotedAt[0] = testT0
	if err := store.Put(ctx, run); err != nil {
		t.Fatal(err)
	}

	clock.Advance(rollout.StallWindow - time.Minute)
	if in := stalledIncident(t, cs); in != nil {
		t.Fatalf("stall raised inside the window: %+v", in)
	}

	clock.Advance(2 * time.Minute)
	in := stalledIncident(t, cs)
	if in == nil {
		t.Fatal("a wave promoted past the stall window raised nothing")
	}
	if !strings.Contains(in.Title, "Canary") {
		t.Errorf("title %q does not name the wave", in.Title)
	}
	if !strings.Contains(in.Detail, "c-1") {
		t.Errorf("detail %q does not name the device that is stuck", in.Detail)
	}
	if strings.Contains(in.Detail, "f-1") {
		t.Errorf("detail %q names a device outside the promoted wave", in.Detail)
	}
	if in.Scope != "org" || in.Tag != "" {
		t.Errorf("a stalled run is fleet-level: %+v", in)
	}

	// Convergence ends it: the wave reached the target, nothing is waiting.
	run.ConvergedAt[0] = clock.Now()
	if err := store.Put(ctx, run); err != nil {
		t.Fatal(err)
	}
	if in := stalledIncident(t, cs); in != nil {
		t.Fatalf("converged wave still reported stalled: %+v", in)
	}
}

// TestComplianceStalledRunWithoutStore: the guard is optional wiring, so a
// console without a rollout store must still serve its per-device incidents
// rather than fail.
func TestComplianceStalledRunWithoutStore(t *testing.T) {
	status := &listStatus{m: map[string]observed.DeviceStatus{"c-1": {Revision: "rev-old"}}}
	cs, _, clock := newComplianceStack(t, status)
	cs.runs = nil
	clock.Advance(10 * rollout.StallWindow)
	if in := stalledIncident(t, cs); in != nil {
		t.Fatalf("stall raised without a rollout store: %+v", in)
	}
}
