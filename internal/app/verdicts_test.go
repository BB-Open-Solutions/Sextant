package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// The memo's whole value is that an unchanged shape is not re-evaluated. Two
// edits that leave a device resolving to the same values must reach the gate
// once, not twice.
func TestVerdictMemoSkipsAnUnchangedShape(t *testing.T) {
	var calls int
	counting := ports.GateFunc(func(_ context.Context, _ string, _ []string) error {
		calls++
		return nil
	})
	svc, _ := newService(t, counting)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"device edit", author, "lt-1"); err != nil {
		t.Fatalf("first edit: %v", err)
	}
	if calls != 1 {
		t.Fatalf("first edit reached the gate %d times, want 1", calls)
	}

	// The org now sets the same key to the same value. lt-1 already resolves
	// it at device scope, so its configuration shape is untouched: nothing to
	// re-prove, and the document still changes (the org scope gained a key),
	// so this is not the no-op shortcut.
	if err := svc.Apply(ctx, fleet.SetScopeSetting("org", "apps.office", true),
		"org edit", author); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if calls != 1 {
		t.Fatalf("an unchanged shape was re-evaluated: gate calls = %d, want 1", calls)
	}

	// The edit must still be committed. A skipped evaluation may not become a
	// skipped write.
	if got := svc.Fleet().Org.Settings["apps.office"]; got != true {
		t.Fatalf("org setting after memoised edit = %v, want true", got)
	}
}

// A changed shape must always be re-evaluated. This is the property that
// makes the memo safe, so it is asserted directly rather than inferred.
func TestVerdictMemoReEvaluatesAChangedShape(t *testing.T) {
	var calls int
	counting := ports.GateFunc(func(_ context.Context, _ string, _ []string) error {
		calls++
		return nil
	})
	svc, _ := newService(t, counting)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	// Three distinct shapes: each must be proved on its own.
	for i, v := range []any{true, false, "mixed"} {
		if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", v),
			"edit", author, "lt-1"); err != nil {
			t.Fatalf("edit %d: %v", i, err)
		}
	}
	if calls != 3 {
		t.Fatalf("gate calls = %d, want 3 (every value change re-evaluates)", calls)
	}

	// Returning to a shape already proved is a hit, not a miss. The memo keys
	// on the configuration, not on the edit history: this document evaluates
	// byte-identically to one the gate already accepted at this same source,
	// so re-proving it would buy nothing.
	if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"back to the first shape", author, "lt-1"); err != nil {
		t.Fatalf("revert edit: %v", err)
	}
	if calls != 3 {
		t.Fatalf("gate calls = %d, want 3 (a shape proved earlier stays proved)", calls)
	}
}

// A rejection must never be remembered. The gate fails once, then accepts;
// the second attempt has to reach it rather than replay a verdict.
func TestVerdictMemoNeverCachesARejection(t *testing.T) {
	var calls int
	flaky := ports.GateFunc(func(_ context.Context, _ string, _ []string) error {
		calls++
		if calls == 1 {
			return &ports.ValidationError{Detail: "nope"}
		}
		return nil
	})
	svc, _ := newService(t, flaky)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"edit", author, "lt-1"); err == nil {
		t.Fatal("first edit: want the gate's rejection, got nil")
	}
	if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"edit", author, "lt-1"); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if calls != 2 {
		t.Fatalf("gate calls = %d, want 2 (a rejection is never memoised)", calls)
	}
}

// The tree outside fleet.json decides a device's build as much as its own
// settings do. A commit that touches it must drop every verdict, or a core
// bump would ride in on evaluations proved against the old one.
func TestVerdictMemoFallsWhenTheRestOfTheTreeChanges(t *testing.T) {
	var calls int
	counting := ports.GateFunc(func(_ context.Context, _ string, _ []string) error {
		calls++
		return nil
	})
	svc, dir := newService(t, counting)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	if err := svc.Apply(ctx, fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"edit", author, "lt-1"); err != nil {
		t.Fatalf("first edit: %v", err)
	}

	// Something else in the flake moves - here a stand-in for flake.lock.
	if err := os.WriteFile(filepath.Join(dir, "flake.lock"), []byte(`{"nodes":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "flake.lock")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "bump core")

	// The same shape as before, and the same value: without the source in the
	// key this would be a hit.
	if err := svc.Apply(ctx, fleet.SetScopeSetting("org", "apps.office", true),
		"org edit", author); err != nil {
		t.Fatalf("second edit: %v", err)
	}
	if calls != 2 {
		t.Fatalf("gate calls = %d, want 2 (a changed tree invalidates verdicts)", calls)
	}
}

// The rollout rings decide which comin branch a device follows
// (nix/generator.nix ringBranchFor), and no device's resolved settings record
// that. Editing them must therefore invalidate verdicts even though every
// class key is unchanged - the case the globals key exists for.
func TestGeneratorGlobalsKeyCoversTheRolloutPlan(t *testing.T) {
	f := &fleet.Fleet{Version: fleet.Version}
	before := f.GeneratorGlobalsKey()

	f.Rollout = &fleet.RolloutPolicy{Rings: []fleet.RolloutRing{{Group: "pilot"}}}
	after := f.GeneratorGlobalsKey()

	if before == after {
		t.Fatal("editing the rollout plan left the globals key unchanged; a ring edit would ride in on stale verdicts")
	}
}

// The mirror property: the fields the resolver folds into a class key must
// NOT move the globals key, or every setting edit would invalidate the whole
// fleet's verdicts and the memo would buy nothing.
func TestGeneratorGlobalsKeyIgnoresResolverInputs(t *testing.T) {
	f := &fleet.Fleet{Version: fleet.Version}
	before := f.GeneratorGlobalsKey()

	f.Org = &fleet.Scope{Settings: map[string]any{"apps.office": true}}
	f.Groups = map[string]fleet.Group{"pilot": {}}
	f.Devices = map[string]fleet.Device{"lt-1": {Hardware: "generic"}}

	if after := f.GeneratorGlobalsKey(); before != after {
		t.Fatal("a scope edit moved the globals key; every edit would then invalidate every shape")
	}
}
