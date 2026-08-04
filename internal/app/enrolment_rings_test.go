package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// fakeRefs records ring-branch moves without a remote.
type fakeRefs struct {
	set    map[string]string
	pushed []string
	head   string
}

func newFakeRefs() *fakeRefs { return &fakeRefs{set: map[string]string{}} }

func (f *fakeRefs) SetRef(_ context.Context, name, rev string) (bool, error) {
	changed := f.set[name] != rev
	f.set[name] = rev
	return changed, nil
}
func (f *fakeRefs) PushRef(_ context.Context, name string) error {
	f.pushed = append(f.pushed, name)
	return nil
}
func (f *fakeRefs) Head(context.Context) (string, error) { return f.head, nil }

// pinGroup pins a group in the live fleet, the way the rollout engine does.
func pinGroup(t *testing.T, svc *ConfigService, group, rev string) {
	t.Helper()
	err := svc.Apply(context.Background(), func(f *fleet.Fleet) error {
		g := f.Groups[group]
		g.Pin = rev
		f.Groups[group] = g
		return nil
	}, "pin "+group, ports.Author{Name: "engine", Email: "e@x"})
	if err != nil {
		t.Fatalf("pin %s: %v", group, err)
	}
}

// The whole point: a device enrolled after the pin does not exist at the pin,
// so its ring has to be fast-forwarded to the commit that created it.
func TestEnsureRingsContainAdvancesTheRing(t *testing.T) {
	svc, dir := newService(t, nil)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	// Pin the group where it stands, then enrol a device after it - exactly
	// the order the console produces.
	pinGroup(t, svc, "pilot", sh(t, dir, "rev-parse", "HEAD"))
	pinned := svc.Fleet().Groups["pilot"].Pin

	if err := svc.Apply(ctx, fleet.AddDevice("lt-2", fleet.Device{
		Groups: []string{"pilot"}, Hardware: "hw",
	}, time.Now()), "enroll lt-2", author, "lt-2"); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	head := sh(t, dir, "rev-parse", "HEAD")
	if head == pinned {
		t.Fatal("enrolment did not produce a new commit; the test proves nothing")
	}

	refs := newFakeRefs()
	moved, err := EnsureRingsContain(ctx, svc, refs, []string{"lt-2"}, head)
	if err != nil {
		t.Fatalf("EnsureRingsContain: %v", err)
	}
	if len(moved) != 1 || moved[0].Group != "pilot" || moved[0].To != head {
		t.Fatalf("advances = %+v, want one move of pilot to %s", moved, head)
	}
	if got := refs.set["rings/pilot"]; got != head {
		t.Fatalf("rings/pilot = %q, want %q", got, head)
	}
	if len(refs.pushed) != 1 {
		t.Fatalf("pushes = %v, want the ring branch pushed once", refs.pushed)
	}
}

// The guard. A ring carrying a real change to an existing member must NOT be
// swept forward by somebody imaging a laptop - it must refuse and say so.
func TestEnsureRingsContainRefusesWhenAMemberWouldChange(t *testing.T) {
	svc, dir := newService(t, nil)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	pinGroup(t, svc, "pilot", sh(t, dir, "rev-parse", "HEAD"))

	// A real configuration change to the group, unrelated to any enrolment.
	if err := svc.Apply(ctx, fleet.SetScopeSetting("group:pilot", "apps.office", true),
		"group edit", author); err != nil {
		t.Fatalf("group edit: %v", err)
	}
	if err := svc.Apply(ctx, fleet.AddDevice("lt-2", fleet.Device{
		Groups: []string{"pilot"}, Hardware: "hw",
	}, time.Now()), "enroll lt-2", author, "lt-2"); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	head := sh(t, dir, "rev-parse", "HEAD")

	refs := newFakeRefs()
	_, err := EnsureRingsContain(ctx, svc, refs, []string{"lt-2"}, head)
	if err == nil {
		t.Fatal("want a refusal: advancing would roll an unrelated change onto lt-1")
	}
	if !strings.Contains(err.Error(), "pilot") || !strings.Contains(err.Error(), "lt-1") {
		t.Fatalf("error must name the ring and the affected device, got: %v", err)
	}
	if len(refs.set) != 0 || len(refs.pushed) != 0 {
		t.Fatalf("a refused advance must move nothing, got set=%v pushed=%v", refs.set, refs.pushed)
	}
}

// A device in a group with no pin has no ring to advance, and must not error.
func TestEnsureRingsContainIgnoresUnpinnedGroups(t *testing.T) {
	svc, dir := newService(t, nil)
	ctx := context.Background()

	if err := svc.Apply(ctx, fleet.AddDevice("lt-2", fleet.Device{
		Groups: []string{"pilot"}, Hardware: "hw",
	}, time.Now()), "enroll lt-2", ports.Author{Name: "Ada", Email: "a@x"}, "lt-2"); err != nil {
		t.Fatalf("enroll: %v", err)
	}
	refs := newFakeRefs()
	moved, err := EnsureRingsContain(ctx, svc, refs, []string{"lt-2"}, sh(t, dir, "rev-parse", "HEAD"))
	if err != nil {
		t.Fatalf("unpinned group must not error: %v", err)
	}
	if len(moved) != 0 || len(refs.set) != 0 {
		t.Fatalf("nothing to advance, got %+v / %v", moved, refs.set)
	}
}
