package app

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/state"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// fakeClock is a settable test clock.
type fakeClock struct{ t atomic.Pointer[time.Time] }

func newFakeClock(t0 time.Time) *fakeClock {
	c := &fakeClock{}
	c.t.Store(&t0)
	return c
}
func (c *fakeClock) Now() time.Time          { return *c.t.Load() }
func (c *fakeClock) Advance(d time.Duration) { n := c.Now().Add(d); c.t.Store(&n) }

// fakeBuilder scripts build outcomes.
type fakeBuilder struct {
	fail  bool
	calls atomic.Int32
}

func (b *fakeBuilder) Build(context.Context, string, []string) error {
	b.calls.Add(1)
	if b.fail {
		return &ports.ValidationError{Detail: "host lt-1 failed to build"}
	}
	return nil
}

var testT0 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

// newChangeStack builds the full change stack over a real git repo.
func newChangeStack(t *testing.T, builder ports.Builder) (*ChangeService, *ConfigService, string) {
	t.Helper()
	svc, dir := newService(t, nil) // from config_test.go
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if builder == nil {
		builder = &fakeBuilder{}
	}
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	cs := NewChangeService(repo, st.Changes(),
		ports.GateFunc(func(context.Context, string, []string) error { return nil }),
		builder, newFakeClock(testT0), open, svc)
	return cs, svc, dir
}

func TestChangeLifecycleHappyPath(t *testing.T) {
	cs, svc, dir := newChangeStack(t, nil)
	ctx := context.Background()

	cr, err := cs.Open(ctx, "office-on", "Enable office for pilot", "ada")
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != change.Draft {
		t.Fatalf("status = %s", cr.Status)
	}

	// Edit on the branch: main must NOT see it yet.
	err = cs.Edit(ctx, "office-on",
		fleet.SetScopeSetting("group:pilot", "apps.office", true),
		"set office", ports.Author{Name: "Ada"})
	if err != nil {
		t.Fatal(err)
	}
	if _, has := svc.Fleet().Resolve("lt-1")["apps.office"]; has {
		t.Fatal("draft edit leaked to main")
	}

	// Submit: builder green -> ready.
	cr, err = cs.Submit(ctx, "office-on")
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != change.Ready {
		t.Fatalf("after submit: %s (%s)", cr.Status, cr.Error)
	}

	// Merge: lands on main, snapshot refreshed, branch gone.
	cr, err = cs.Merge(ctx, "office-on", ports.Author{Name: "Bob", Email: "bob@x"})
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != change.Merged {
		t.Fatalf("after merge: %s", cr.Status)
	}
	if v := svc.Fleet().Resolve("lt-1")["apps.office"]; v.Value != true {
		t.Fatal("merged edit not visible on main")
	}
	// Branch deleted; merge commit present.
	if out := sh(t, dir, "branch", "--list", "cr/office-on"); out != "" {
		t.Errorf("branch not cleaned up: %q", out)
	}
	if log := sh(t, dir, "log", "--oneline", "-3"); !strings.Contains(log, "merge change office-on") {
		t.Errorf("no merge commit: %s", log)
	}
}

func TestChangeBuildFailure(t *testing.T) {
	b := &fakeBuilder{fail: true}
	cs, _, _ := newChangeStack(t, b)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "bad", "Broken change", "ada"); err != nil {
		t.Fatal(err)
	}
	cr, err := cs.Submit(ctx, "bad")
	if err != nil {
		t.Fatal(err)
	}
	if cr.Status != change.Failed || !strings.Contains(cr.Error, "failed to build") {
		t.Fatalf("cr = %+v", cr)
	}
	// A failed change cannot merge.
	if _, err := cs.Merge(ctx, "bad", ports.Author{}); err == nil {
		t.Fatal("failed change merged")
	}
	// Rework: an edit moves it back to draft and clears the error.
	if err := cs.Edit(ctx, "bad", fleet.SetScopeSetting("org", "x", 1), "fix", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	cr, _, _ = cs.Get(ctx, "bad")
	if cr.Status != change.Draft || cr.Error != "" {
		t.Fatalf("after rework: %+v", cr)
	}
	// Resubmit with the builder still failing -> failed again.
	b.fail = false
	cr, err = cs.Submit(ctx, "bad")
	if err != nil || cr.Status != change.Ready {
		t.Fatalf("resubmit = %+v, %v", cr, err)
	}
}

func TestChangeGateRejectionOnEdit(t *testing.T) {
	svc, dir := newService(t, nil)
	repo, _ := git.Open(dir, "")
	st, _ := state.Open(t.TempDir())
	rejecting := ports.GateFunc(func(context.Context, string, []string) error {
		return &ports.ValidationError{Detail: "bogus option"}
	})
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	cs := NewChangeService(repo, st.Changes(), rejecting, &fakeBuilder{}, newFakeClock(testT0), open, svc)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "gated", "Gated", "ada"); err != nil {
		t.Fatal(err)
	}
	err := cs.Edit(ctx, "gated", fleet.SetScopeSetting("org", "apps.bogus", true), "bad", ports.Author{})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %v", err)
	}
}

func TestChangeGuards(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "dup", "One", "a"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Open(ctx, "dup", "Two", "a"); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if _, err := cs.Open(ctx, "../inject", "Bad", "a"); err == nil {
		t.Fatal("unsafe id accepted")
	}
	// Draft cannot merge (merged only via ready).
	if _, err := cs.Merge(ctx, "dup", ports.Author{}); err == nil {
		t.Fatal("draft merged")
	}
	// Unknown id.
	if _, err := cs.Submit(ctx, "ghost"); err == nil {
		t.Fatal("unknown change submitted")
	}
	// Abandon closes and cleans up; edits refused afterwards.
	if _, err := cs.Abandon(ctx, "dup"); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "dup", fleet.SetScopeSetting("org", "x", 1), "m", ports.Author{}); err == nil {
		t.Fatal("edit on abandoned change accepted")
	}
}

func TestChangeSurvivesRestart(t *testing.T) {
	svc, dir := newService(t, nil)
	repo, _ := git.Open(dir, "")
	stateDir := t.TempDir()
	st, _ := state.Open(stateDir)
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	allow := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	cs := NewChangeService(repo, st.Changes(), allow, &fakeBuilder{}, newFakeClock(testT0), open, svc)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "persist", "Survives", "ada"); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "persist", fleet.SetScopeSetting("org", "y", 2), "m", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	// New service instance over the same state dir and repo (a restart).
	st2, _ := state.Open(stateDir)
	cs2 := NewChangeService(repo, st2.Changes(), allow, &fakeBuilder{}, newFakeClock(testT0), open, svc)
	cr, ok, err := cs2.Get(ctx, "persist")
	if err != nil || !ok || cr.Status != change.Draft {
		t.Fatalf("after restart: %+v %v %v", cr, ok, err)
	}
	// The flow continues where it left off.
	if cr, err = cs2.Submit(ctx, "persist"); err != nil || cr.Status != change.Ready {
		t.Fatalf("submit after restart: %+v %v", cr, err)
	}
	if _, err := cs2.Merge(ctx, "persist", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if v := svc.Fleet().Org.Settings["y"]; v != float64(2) && v != 2 {
		t.Fatalf("merged value = %v", v)
	}
}
