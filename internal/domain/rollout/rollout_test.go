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
	// 1 of 5 devices unhealthy on target = 20%, over the 5% failure budget
	// of the default 95% gate: a bad release halts rather than waits.
	act := Decide(rings, s, RingStatus{Total: 5, OnTarget: 4, Healthy: 3, Broken: 1}, t0.Add(time.Minute))
	if act.Kind != Halt {
		t.Fatalf("act = %+v, want halt", act)
	}
	if !strings.Contains(act.Reason, "failure budget") {
		t.Errorf("reason should name the failure budget: %s", act.Reason)
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

func TestRingNextRelease(t *testing.T) {
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

func TestThresholdPromotesWithStragglers(t *testing.T) {
	// 20 devices, 19 healthy on target = 95%: meets the default gate even
	// though one straggler never converged - the wave soaks and advances.
	defaults := []Ring{{Group: "wave1"}, {Group: "wave2"}} // gate defaults to 95
	s := NewState("rev-2", t0)
	s.PromotedAt[0] = t0
	s.ConvergedAt[0] = t0
	act := Decide(defaults, s, RingStatus{Total: 20, OnTarget: 19, Healthy: 19, Released: 20, GroupTotal: 20}, t0.Add(2*time.Hour))
	if act.Kind != Advance && act.Kind != AwaitApproval {
		t.Fatalf("act = %+v, want advance/await past the straggler", act)
	}
}

func TestPausedRunDecidesNothing(t *testing.T) {
	s := NewState("rev-2", t0)
	s.Status = Paused
	act := Decide(rings, s, RingStatus{}, t0)
	if act.Kind != Done || !strings.Contains(act.Reason, "paused") {
		t.Fatalf("act = %+v, want inert done/paused", act)
	}
}

func TestConvergedIgnoresAbsentDevices(t *testing.T) {
	r := Ring{Group: "g"} // default 95%
	// 10 devices, 4 shut laptops (holiday): the wave proves itself on the
	// 6 present; all 6 healthy = converged despite the absentees.
	rs := RingStatus{Total: 10, Absent: 4, OnTarget: 6, Healthy: 6}
	if !r.Converged(rs) {
		t.Fatal("absent devices must not hold the wave")
	}
	// One of the present is unhealthy: 5/6 = 83% < 95, not converged.
	rs.Healthy = 5
	if r.Converged(rs) {
		t.Fatal("present unhealthy devices still count")
	}
	// Entire cohort absent: nothing proven, never converges.
	all := RingStatus{Total: 3, Absent: 3}
	if r.Converged(all) {
		t.Fatal("an entirely absent cohort proves nothing")
	}
	if all.Present() != 0 {
		t.Fatalf("present = %d", all.Present())
	}
}

func TestTooBrokenUsesPresentDenominator(t *testing.T) {
	r := Ring{Group: "g"} // default 95%: failure budget 5%
	// 10 total, 8 absent, 2 present: one demonstrably broken present device
	// = 50% of the present population - far past the budget, halt.
	rs := RingStatus{Total: 10, Absent: 8, OnTarget: 2, Healthy: 1, Broken: 1}
	if !r.TooBroken(rs) {
		t.Fatal("broken share of the present population must trigger")
	}
	// Entirely absent cohort can never be "too broken".
	if (Ring{Group: "g"}).TooBroken(RingStatus{Total: 5, Absent: 5}) {
		t.Fatal("absent cohort is not broken, just away")
	}
}

// TestTooBrokenIgnoresDozingOnTarget pins the window seam: a device that
// reached the target and then went quiet for a few minutes sits in OnTarget
// but not in Healthy - it is asleep, not broken, and must not trip the
// failure budget. Only demonstrated failure (Broken) counts.
func TestTooBrokenIgnoresDozingOnTarget(t *testing.T) {
	r := Ring{Group: "g"}
	// 4 present: 3 on target, 1 healthy right now, 2 merely quiet, 1 with a
	// real error. Broken=1 of 4 = 25% > 5%: halts on the REAL failure...
	rs := RingStatus{Total: 4, OnTarget: 3, Healthy: 1, Broken: 1}
	if !r.TooBroken(rs) {
		t.Fatal("a real error past the budget must halt")
	}
	// ...but with no demonstrated failure, dozing on-target devices alone
	// must never read as a bad release.
	rs.Broken = 0
	if r.TooBroken(rs) {
		t.Fatal("quiet on-target devices are asleep, not broken")
	}
}

func TestStalledFor(t *testing.T) {
	cases := []struct {
		name  string
		build func() *State
		now   time.Time
		want  time.Duration
	}{
		{
			name:  "never promoted",
			build: func() *State { return NewState("rev-2", t0) },
			now:   t0.Add(2 * time.Hour),
			want:  0,
		},
		{
			name: "just promoted",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				return s
			},
			now:  t0.Add(time.Minute),
			want: time.Minute,
		},
		{
			name: "promoted and converged",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				s.ConvergedAt[0] = t0.Add(10 * time.Minute)
				return s
			},
			now:  t0.Add(5 * time.Hour),
			want: 0,
		},
		{
			name: "promoted, never converged, past the window",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				return s
			},
			now:  t0.Add(StallWindow + time.Minute),
			want: StallWindow + time.Minute,
		},
		{
			name: "later ring stalls on its own clock",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.Ring = 1
				s.PromotedAt[0], s.ConvergedAt[0] = t0, t0.Add(time.Minute)
				s.PromotedAt[1] = t0.Add(time.Hour)
				return s
			},
			now:  t0.Add(3 * time.Hour),
			want: 2 * time.Hour,
		},
		{
			name: "paused run is already visible, not a silent wait",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				s.Status = Paused
				return s
			},
			now:  t0.Add(5 * time.Hour),
			want: 0,
		},
		{
			name: "halted run does not stall",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				s.Status = Halted
				return s
			},
			now:  t0.Add(5 * time.Hour),
			want: 0,
		},
		{
			name: "clock behind the promotion",
			build: func() *State {
				s := NewState("rev-2", t0)
				s.PromotedAt[0] = t0
				return s
			},
			now:  t0.Add(-time.Minute),
			want: 0,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := c.build()
			if got := s.StalledFor(c.now, s.Ring); got != c.want {
				t.Fatalf("StalledFor = %s, want %s", got, c.want)
			}
		})
	}
}

func TestStalledForNilState(t *testing.T) {
	var s *State
	if got := s.StalledFor(t0, 0); got != 0 {
		t.Fatalf("StalledFor on nil = %s, want 0", got)
	}
}
