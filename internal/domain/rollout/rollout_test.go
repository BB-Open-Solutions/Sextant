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
