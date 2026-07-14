package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
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

	cr, err := cs.Open(ctx, "office-on", "Enable office for pilot", ports.Author{Name: "ada", Subject: "sub-ada"})
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

	if _, err := cs.Open(ctx, "bad", "Broken change", ports.Author{Name: "ada", Subject: "sub-ada"}); err != nil {
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

	if _, err := cs.Open(ctx, "gated", "Gated", ports.Author{Name: "ada", Subject: "sub-ada"}); err != nil {
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

	if _, err := cs.Open(ctx, "dup", "One", ports.Author{Name: "a", Subject: "sub-a"}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Open(ctx, "dup", "Two", ports.Author{Name: "a", Subject: "sub-a"}); err == nil {
		t.Fatal("duplicate id accepted")
	}
	if _, err := cs.Open(ctx, "../inject", "Bad", ports.Author{Name: "a"}); err == nil {
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

	if _, err := cs.Open(ctx, "persist", "Survives", ports.Author{Name: "ada", Subject: "sub-ada"}); err != nil {
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

func TestChangeDiff(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()
	if _, err := cs.Open(ctx, "diffy", "Show me", ports.Author{Name: "ada", Subject: "sub-ada"}); err != nil {
		t.Fatal(err)
	}
	// No edits yet: empty diff, no error.
	if d, err := cs.Diff(ctx, "diffy"); err != nil || strings.TrimSpace(d) != "" {
		t.Fatalf("empty diff = %q, %v", d, err)
	}
	if err := cs.Edit(ctx, "diffy",
		fleet.SetScopeSetting("group:pilot", "apps.office", true), "edit", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	d, err := cs.Diff(ctx, "diffy")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(d, "apps.office") || !strings.Contains(d, "+") {
		t.Fatalf("diff misses the edit: %s", d)
	}
	// Merged change has no pending diff.
	if _, err := cs.Submit(ctx, "diffy"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Merge(ctx, "diffy", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Diff(ctx, "diffy"); err == nil {
		t.Fatal("diff on merged change accepted")
	}
	if _, err := cs.Diff(ctx, "ghost"); err == nil {
		t.Fatal("diff on unknown change accepted")
	}
}

// failPutStore wraps a real ChangeStore and fails the next Put call once,
// so tests can exercise a transient persistence failure mid-Open.
type failPutStore struct {
	ports.ChangeStore
	failNextPut bool
}

func (f *failPutStore) Put(ctx context.Context, cr change.CR) error {
	if f.failNextPut {
		f.failNextPut = false
		return errors.New("store unavailable")
	}
	return f.ChangeStore.Put(ctx, cr)
}

// TestOpenRollsBackBranchOnStoreFailure (finding: Open() wedges a change id
// when CreateBranch succeeds but store.Put fails): a transient store error
// after the branch is created must not leave an orphaned branch behind - the
// id must stay retryable.
func TestOpenRollsBackBranchOnStoreFailure(t *testing.T) {
	svc, dir := newService(t, nil)
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	st, err := state.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	store := &failPutStore{ChangeStore: st.Changes(), failNextPut: true}
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	cs := NewChangeService(repo, store,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }),
		&fakeBuilder{}, newFakeClock(testT0), open, svc)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "wedge", "First attempt", ports.Author{Name: "ada", Subject: "sub-ada"}); err == nil {
		t.Fatal("want the store failure surfaced")
	}
	if out := sh(t, dir, "branch", "--list", "cr/wedge"); out != "" {
		t.Fatalf("branch not rolled back after store.Put failure: %q", out)
	}
	if _, ok, err := store.Get(ctx, "wedge"); err != nil || ok {
		t.Fatalf("no record should exist after rollback: ok=%v err=%v", ok, err)
	}

	// The id must not be permanently wedged: a retry with the store healthy
	// again succeeds cleanly.
	cr, err := cs.Open(ctx, "wedge", "Retry", ports.Author{Name: "ada", Subject: "sub-ada"})
	if err != nil {
		t.Fatalf("retry after rollback failed: %v", err)
	}
	if cr.Status != change.Draft {
		t.Fatalf("status = %s", cr.Status)
	}
}

// TestFourEyesEnforced (ADR 0007): with assurance.requireFourEyes, the
// author of a change cannot merge it; a different owner can. Without the
// flag, self-merge stays allowed (small orgs).
func TestFourEyesEnforced(t *testing.T) {
	const seed4 = `{
	  "version": 3,
	  "assurance": {"requireFourEyes": true},
	  "org": {"settings": {"desktop": "plasma"}},
	  "groups": {"pilot": {}},
	  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
	}`
	dir := t.TempDir()
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seed4), 0o644); err != nil {
		t.Fatal(err)
	}
	shr("add", "fleet.json")
	shr("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	allow := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	svc, err := NewConfigService(repo, allow)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(t.TempDir())
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	cs := NewChangeService(repo, st.Changes(), allow, &fakeBuilder{}, newFakeClock(testT0), open, svc)
	ctx := context.Background()

	ada := ports.Author{Name: "Ada", Email: "ada@x", Subject: "sub-ada"}
	bob := ports.Author{Name: "Bob", Email: "bob@x", Subject: "sub-bob"}

	if _, err := cs.Open(ctx, "sod", "SoD test", ada); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "sod", fleet.SetScopeSetting("org", "x", 1), "e", ada); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, "sod"); err != nil {
		t.Fatal(err)
	}
	// Author self-merge: refused.
	if _, err := cs.Merge(ctx, "sod", ada); err == nil ||
		!strings.Contains(err.Error(), "four-eyes") {
		t.Fatalf("self-merge = %v, want four-eyes rejection", err)
	}
	// A different owner merges fine.
	if _, err := cs.Merge(ctx, "sod", bob); err != nil {
		t.Fatalf("second-person merge failed: %v", err)
	}
}

// TestFourEyesFailsClosedOnEmptySubject: an unidentifiable principal (empty
// subject) can never satisfy segregation of duties; the merge must be refused,
// not waved through because equality could not be established.
func TestFourEyesFailsClosedOnEmptySubject(t *testing.T) {
	const seed4 = `{
	  "version": 3,
	  "assurance": {"requireFourEyes": true},
	  "org": {"settings": {"desktop": "plasma"}},
	  "groups": {"pilot": {}},
	  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
	}`
	dir := t.TempDir()
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seed4), 0o644); err != nil {
		t.Fatal(err)
	}
	shr("add", "fleet.json")
	shr("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	allow := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	svc, err := NewConfigService(repo, allow)
	if err != nil {
		t.Fatal(err)
	}
	st, _ := state.Open(t.TempDir())
	open := func(d string) (ports.ConfigRepo, error) { return git.Open(d, "") }
	cs := NewChangeService(repo, st.Changes(), allow, &fakeBuilder{}, newFakeClock(testT0), open, svc)
	ctx := context.Background()

	// Author with NO subject opens and submits the change.
	anon := ports.Author{Name: "Anon", Email: "anon@x"}
	if _, err := cs.Open(ctx, "anon-cr", "anon", anon); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "anon-cr", fleet.SetScopeSetting("org", "x", 1), "e", anon); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, "anon-cr"); err != nil {
		t.Fatal(err)
	}
	// An approver who is also unidentifiable must NOT be able to merge it.
	if _, err := cs.Merge(ctx, "anon-cr", ports.Author{Name: "Ghost"}); err == nil ||
		!strings.Contains(err.Error(), "four-eyes") {
		t.Fatalf("empty-subject merge = %v, want four-eyes rejection", err)
	}
}
