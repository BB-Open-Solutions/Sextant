package app

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// rollout_control_test.go covers the operator's control surface over a
// running wave: pause, resume, approve, and the straggler lookup that names
// what is holding a wave up. All four were at 0%.
//
// They are the buttons somebody reaches for when a rollout is going wrong,
// which makes "pause did not pause" a failure discovered at the worst
// possible moment. The engine reads Status on every tick, so what these
// assert is not that a field changed but that the ENGINE stops acting.

func TestPauseStopsTheEngineAndResumeStartsItAgain(t *testing.T) {
	rs, _, conv, clock, _ := newRolloutStack(t)
	ctx := context.Background()
	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	// First tick promotes ring 0; the run is properly under way.
	if got := tick(t, rs); got != rollout.Promote {
		t.Fatalf("first tick = %v, want Promote", got)
	}

	st, err := rs.Pause(ctx)
	if err != nil {
		t.Fatalf("pause: %v", err)
	}
	if st.Status != rollout.Paused {
		t.Fatalf("status = %v, want Paused", st.Status)
	}
	// The reason is what the console shows. An empty one leaves an operator
	// looking at a stopped rollout with no explanation.
	if st.Reason == "" {
		t.Error("a paused run carries no reason")
	}

	// The point of the pause: even with the ring fully converged and the soak
	// elapsed - conditions that would promote - the engine does nothing.
	conv.set("canary", rollout.RingStatus{Total: 1, OnTarget: 1})
	clock.Advance(2 * time.Hour) // well past the canary's 60-minute soak
	act, after, err := rs.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if act != nil {
		t.Errorf("a paused run acted: %v", act.Kind)
	}
	if after.Status != rollout.Paused {
		t.Errorf("status drifted to %v while paused", after.Status)
	}

	// Pausing twice is an error rather than a silent no-op: an operator who
	// gets "ok" from a pause on an already-halted run believes they stopped
	// something.
	if _, err := rs.Pause(ctx); err == nil {
		t.Error("pausing an already paused run reported success")
	}

	if st, err = rs.Resume(ctx); err != nil {
		t.Fatalf("resume: %v", err)
	}
	if st.Status != rollout.Active {
		t.Fatalf("status after resume = %v", st.Status)
	}
	if st.Reason != "" {
		t.Errorf("resume left the pause reason behind: %q", st.Reason)
	}
	// And now the engine acts again, on the same conditions it ignored above.
	act, _, err = rs.Tick(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if act == nil {
		t.Error("the engine stayed idle after resume")
	}
}

func TestPauseAndResumeRefuseWhenThereIsNothingToDo(t *testing.T) {
	rs, _, _, _, _ := newRolloutStack(t)
	ctx := context.Background()

	if _, err := rs.Pause(ctx); err == nil {
		t.Error("pausing with no run at all reported success")
	}
	if _, err := rs.Resume(ctx); err == nil {
		t.Error("resuming with no run at all reported success")
	}

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	// Resuming an ACTIVE run is not harmless bookkeeping: it would clear the
	// reason field on a run that might be halted for cause.
	if _, err := rs.Resume(ctx); err == nil {
		t.Error("resuming an active run reported success")
	}
}

// TestApproveIsRefusedWithoutAnActiveRun: approval is the four-eyes gate on
// a wave. Approving into nothing must fail loudly rather than record consent
// that no run will ever read.
func TestApproveIsRefusedWithoutAnActiveRun(t *testing.T) {
	rs, _, _, _, _ := newRolloutStack(t)
	ctx := context.Background()

	if _, err := rs.Approve(ctx); err == nil {
		t.Error("approving with no run reported success")
	}

	if _, err := rs.Start(ctx, "rev-2", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	st, err := rs.Approve(ctx)
	if err != nil {
		t.Fatalf("approve on an active run: %v", err)
	}
	if st == nil {
		t.Fatal("approve returned no state")
	}
	// A pause must not swallow a recorded approval: the operator approved the
	// wave, and holding the run is a separate decision from withdrawing that.
	if _, err := rs.Pause(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := rs.Approve(ctx); err == nil {
		t.Error("approving a paused run reported success")
	}
}

// TestStragglersIsSilentWhenTheSourceCannotAnswer: the convergence source is
// an interface a deployment may satisfy only partly. A straggler lookup that
// cannot run must return nothing, not panic and not invent devices - an
// operator reading "no stragglers" from a source that was never asked is
// a smaller harm than a crashed rollout page, and the log carries the truth.
func TestStragglersIsSilentWhenTheSourceCannotAnswer(t *testing.T) {
	rs, _, _, _, _ := newRolloutStack(t)
	// fakeConvergence does not implement stragglerSource.
	if got := rs.Stragglers(context.Background(), []string{"canary"}, "rev-2"); got != nil {
		t.Errorf("stragglers from a source that cannot answer = %v, want nil", got)
	}
}
