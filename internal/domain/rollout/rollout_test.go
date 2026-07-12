package rollout

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

var rings = []Ring{
	{Group: "canary", SoakMinutes: 60, MinHealthyPercent: 100},
	{Group: "fleet", SoakMinutes: 0, MinHealthyPercent: 90},
}

func TestPromoteFirst(t *testing.T) {
	s := NewState("rev-2", t0)
	act := Decide(rings, s, RingStatus{}, t0)
	if act.Kind != Promote || !strings.Contains(act.Reason, "canary") {
		t.Fatalf("act = %+v, want promote canary", act)
	}
}

func TestWaitWhileConverging(t *testing.T) {
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	act := Decide(rings, s, RingStatus{Total: 5, OnTarget: 2, Healthy: 2}, t0.Add(time.Minute))
	if act.Kind != Wait || !strings.Contains(act.Reason, "2/5") {
		t.Fatalf("act = %+v, want wait 2/5", act)
	}
}

func TestHaltOnUnhealthy(t *testing.T) {
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	// 3 of 4 converged devices healthy = 75% < 100% gate.
	act := Decide(rings, s, RingStatus{Total: 5, OnTarget: 4, Healthy: 3}, t0.Add(time.Minute))
	if act.Kind != Halt {
		t.Fatalf("act = %+v, want halt", act)
	}
	if !strings.Contains(act.Reason, "75%") {
		t.Errorf("reason should carry the number: %s", act.Reason)
	}
}

func TestSecondRingGateIsLenient(t *testing.T) {
	s := NewState("rev-2", t0)
	s.Ring = 1
	s.PromotedAt[1] = t0
	// 9 of 10 healthy = 90%, meets the ring's 90% gate: no halt, wait for
	// the last device to converge.
	act := Decide(rings, s, RingStatus{Total: 11, OnTarget: 10, Healthy: 9}, t0.Add(time.Minute))
	if act.Kind != Wait {
		t.Fatalf("act = %+v, want wait (gate met)", act)
	}
}

func TestSoakThenAdvance(t *testing.T) {
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	full := RingStatus{Total: 5, OnTarget: 5, Healthy: 5}

	// First observation of full convergence: engine says wait (soak starts);
	// the service records ConvergedAt.
	act := Decide(rings, s, full, t0.Add(10*time.Minute))
	if act.Kind != Wait || !strings.Contains(act.Reason, "soak") {
		t.Fatalf("act = %+v, want wait starting soak", act)
	}
	s.ConvergedAt[0] = t0.Add(10 * time.Minute)

	// Mid-soak: still waiting.
	if act := Decide(rings, s, full, t0.Add(30*time.Minute)); act.Kind != Wait {
		t.Fatalf("mid-soak act = %+v", act)
	}
	// Soak (60m) elapsed: advance to ring 1.
	if act := Decide(rings, s, full, t0.Add(71*time.Minute)); act.Kind != Advance {
		t.Fatalf("post-soak act = %+v, want advance", act)
	}
}

func TestLastRingDone(t *testing.T) {
	s := NewState("rev-2", t0)
	s.Ring = 1
	s.PromotedAt[1] = t0
	s.ConvergedAt[1] = t0 // soak 0 on ring 1
	act := Decide(rings, s, RingStatus{Total: 10, OnTarget: 10, Healthy: 10}, t0.Add(time.Minute))
	if act.Kind != Done {
		t.Fatalf("act = %+v, want done", act)
	}
}

func TestEmptyRingAdvances(t *testing.T) {
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	act := Decide(rings, s, RingStatus{}, t0.Add(time.Minute))
	if act.Kind != Advance || !strings.Contains(act.Reason, "no devices") {
		t.Fatalf("act = %+v, want advance (empty ring)", act)
	}
}

func TestInactiveRunIsDone(t *testing.T) {
	s := NewState("rev-2", t0)
	s.Status = Halted
	if act := Decide(rings, s, RingStatus{}, t0); act.Kind != Done {
		t.Fatalf("halted run act = %+v", act)
	}
	s2 := NewState("rev-2", t0)
	s2.Ring = 99
	if act := Decide(rings, s2, RingStatus{}, t0); act.Kind != Done {
		t.Fatalf("out-of-range ring act = %+v", act)
	}
}

func TestManualGateAwaitsApproval(t *testing.T) {
	// A test wave that requires sign-off: healthy and soaked, but a manual
	// gate holds promotion until approved.
	waves := []Ring{
		{Group: "canary", Name: "Test #1", SoakMinutes: 0, RequireApproval: true},
		{Group: "fleet", Name: "Phase 1"},
	}
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	s.ConvergedAt[0] = t0 // converged, soak is 0 so it has elapsed

	full := RingStatus{Total: 3, OnTarget: 3, Healthy: 3}
	act := Decide(waves, s, full, t0.Add(time.Minute))
	if act.Kind != AwaitApproval || !strings.Contains(act.Reason, "Test #1") {
		t.Fatalf("act = %+v, want await-approval for Test #1", act)
	}

	// After operator sign-off, the pipeline promotes the next wave.
	s.Ring = 0
	s.Approve(t0.Add(2 * time.Minute))
	act = Decide(waves, s, full, t0.Add(3*time.Minute))
	if act.Kind != Advance {
		t.Fatalf("act = %+v, want advance after approval", act)
	}
}

func TestRingLabelFallsBackToGroup(t *testing.T) {
	if got := (Ring{Group: "fleet"}).Label(); got != "fleet" {
		t.Fatalf("Label() = %q, want group name", got)
	}
	if got := (Ring{Group: "fleet", Name: "Phase 1"}).Label(); got != "Phase 1" {
		t.Fatalf("Label() = %q, want name", got)
	}
}

func TestRingCohort(t *testing.T) {
	devs := []string{"a", "b", "c", "d", "e"}
	// Uncapped: whole group.
	if got := (Ring{}).Cohort(devs, 2); len(got) != 5 {
		t.Fatalf("uncapped cohort = %v, want all 5", got)
	}
	// Capped, released clamps to [0,len].
	r := Ring{MaxDevices: 2}
	if got := r.Cohort(devs, 0); len(got) != 0 {
		t.Fatalf("released 0 = %v, want none", got)
	}
	if got := r.Cohort(devs, 2); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("released 2 = %v, want [a b]", got)
	}
	if got := r.Cohort(devs, 99); len(got) != 5 {
		t.Fatalf("over-release should clamp to 5, got %v", got)
	}
	if got := r.Cohort(devs, -3); len(got) != 0 {
		t.Fatalf("negative release should clamp to 0, got %v", got)
	}
}

func TestRingNextReleaseAndFullyReleased(t *testing.T) {
	// Uncapped jumps straight to the whole group.
	if n := (Ring{}).NextRelease(10, 0); n != 10 {
		t.Fatalf("uncapped next = %d, want 10", n)
	}
	// Capped widens by MaxDevices, then clamps at total: 0 -> 2 -> 4 ... -> 5.
	r := Ring{MaxDevices: 2}
	if n := r.NextRelease(5, 0); n != 2 {
		t.Fatalf("first cohort = %d, want 2", n)
	}
	if n := r.NextRelease(5, 4); n != 5 {
		t.Fatalf("last cohort should clamp to 5, got %d", n)
	}
	if !r.FullyReleased(5, 5) || r.FullyReleased(5, 4) {
		t.Fatal("FullyReleased wrong")
	}
}

func TestDecideCappedCohortWidens(t *testing.T) {
	// One capped wave: group of 10, release 2 at a time.
	capped := []Ring{{Group: "fleet", SoakMinutes: 0, MinHealthyPercent: 100, MaxDevices: 2}}
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0

	// Current cohort of 2 fully converged + healthy; group has 10, so 8 remain.
	rs := RingStatus{Total: 2, OnTarget: 2, Healthy: 2, Released: 2, GroupTotal: 10}
	// Not yet soaked-recorded -> Wait (starting soak).
	if act := Decide(capped, s, rs, t0); act.Kind != Wait {
		t.Fatalf("first = %s, want wait", act.Kind)
	}
	s.ConvergedAt[0] = t0
	// Soak is 0, so the cohort is soaked -> widen (release the next batch).
	if act := Decide(capped, s, rs, t0.Add(time.Minute)); act.Kind != WidenCohort {
		t.Fatalf("capped cohort = %s, want widen-cohort", act.Kind)
	}
}

func TestDecideCappedFullyReleasedAdvances(t *testing.T) {
	capped := []Ring{{Group: "fleet", MaxDevices: 2}}
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	s.ConvergedAt[0] = t0
	// Released == GroupTotal: the whole group is out -> last wave Done.
	rs := RingStatus{Total: 10, OnTarget: 10, Healthy: 10, Released: 10, GroupTotal: 10}
	if act := Decide(capped, s, rs, t0.Add(time.Minute)); act.Kind != Done {
		t.Fatalf("fully released = %s, want done", act.Kind)
	}
}

func TestDecideUncappedNeverWidens(t *testing.T) {
	// Uncapped wave (MaxDevices 0): caller sets Released == GroupTotal == Total.
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	s.ConvergedAt[0] = t0
	rs := RingStatus{Total: 5, OnTarget: 5, Healthy: 5, Released: 5, GroupTotal: 5}
	// canary is uncapped + soaked -> advance to the next wave, never widen.
	if act := Decide(rings, s, rs, t0.Add(61*time.Minute)); act.Kind != Advance {
		t.Fatalf("uncapped = %s, want advance", act.Kind)
	}
}
