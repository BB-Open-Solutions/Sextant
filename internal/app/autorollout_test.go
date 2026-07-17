package app

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// mergeChange drives a change through open -> edit -> submit -> merge on the
// shared stack.
func mergeChange(t *testing.T, cs *ChangeService, id string) {
	t.Helper()
	ctx := context.Background()
	if _, err := cs.Open(ctx, id, "t", ports.Author{Name: "ada", Subject: "s1"}); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, id, fleet.SetScopeSetting("group:pilot", "apps.office", true),
		"set", ports.Author{Name: "ada"}, "lt-1"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Merge(ctx, id, ports.Author{Name: "bob", Subject: "s2"}); err != nil {
		t.Fatal(err)
	}
}

func TestSavingIsRollingOut(t *testing.T) {
	cs, svc, _ := newChangeStack(t, nil)
	ctx := context.Background()

	// The org ladder: pilot doubles as the test group in this small fleet.
	if err := svc.Apply(ctx, fleet.SetRolloutPlan(&fleet.RolloutPolicy{Rings: []fleet.RolloutRing{
		{Group: "pilot", Name: "Testgroep", RequireApproval: true, SoakMinutes: 60},
	}}), "plan", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rs := NewRolloutService(svc, st.Rollouts(), &fakeConvergence{}, newFakeClock(testT0),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	WireAutoRollout(cs, rs, svc, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mergeChange(t, cs, "office-on")

	run, err := st.Rollouts().Get(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if run == nil || run.Status != rollout.Active {
		t.Fatalf("merge did not start a delivery run: %+v", run)
	}
	// lt-1 lives in pilot, which IS the test group: one wave suffices.
	if len(run.Rings) != 1 || run.Rings[0].Group != "pilot" {
		t.Fatalf("run rings = %+v, want just the pilot test wave", run.Rings)
	}
	if run.Target != svc.Head(ctx) || run.Target == "" {
		t.Fatalf("run target = %q, head = %q", run.Target, svc.Head(ctx))
	}
	if run.ChangeID != "office-on" || run.ChangeTitle == "" {
		t.Fatalf("run does not name its change: %q %q", run.ChangeID, run.ChangeTitle)
	}
}

func TestManualRolloutOnlySkipsAutoDelivery(t *testing.T) {
	cs, svc, _ := newChangeStack(t, nil)
	ctx := context.Background()
	if err := svc.Apply(ctx, fleet.SetRolloutPlan(&fleet.RolloutPolicy{Rings: []fleet.RolloutRing{
		{Group: "pilot", Name: "Testgroep", RequireApproval: true},
	}}), "plan", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if err := svc.Apply(ctx, fleet.SetAssurance(fleet.Assurance{ManualRolloutOnly: true}),
		"assurance", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rs := NewRolloutService(svc, st.Rollouts(), &fakeConvergence{}, newFakeClock(testT0),
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	WireAutoRollout(cs, rs, svc, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))

	mergeChange(t, cs, "office-on")

	if run, _ := st.Rollouts().Get(ctx); run != nil {
		t.Fatalf("manual-only org still auto-started a run: %+v", run)
	}
}
