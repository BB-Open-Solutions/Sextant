package app

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

var submitAuthor = ports.Author{Name: "ada", Subject: "ada-subject"}

// blockingGate parks inside Validate until released, so a test can inspect the
// service while a submit is mid-evaluation - which is where a real nix eval
// spends seconds to minutes.
type blockingGate struct {
	entered chan struct{}
	release chan struct{}
	// armed lets a test give its change a commit before the gate starts
	// blocking: an empty change is refused before the gate is reached.
	armed atomic.Bool
}

func newBlockingGate() *blockingGate {
	return &blockingGate{entered: make(chan struct{}, 1), release: make(chan struct{})}
}

// arm makes the gate start blocking. Before that it waves everything through,
// so a test can give its change an actual commit first - an empty change is
// refused before the gate is reached, and this file is about the lock, not
// about that rule.
func (g *blockingGate) arm() { g.armed.Store(true) }

func (g *blockingGate) Validate(context.Context, string, []string) error {
	if !g.armed.Load() {
		return nil
	}
	select {
	case g.entered <- struct{}{}:
	default:
	}
	<-g.release
	return nil
}

// The service lock must NOT be held while the gate runs. It exists for git
// worktree operations, not for the evaluation, and holding it across a nix eval
// meant one submitting change froze the whole change pipeline for everyone -
// the one finding from an external review that was both real and about a
// bottleneck rather than a bug.
func TestSubmitDoesNotHoldTheLockDuringTheGate(t *testing.T) {
	gate := newBlockingGate()
	cs, _, _ := newChangeStackWithGate(t, gate)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "cr-slow", "slow change", submitAuthor); err != nil {
		t.Fatal(err)
	}
	// The change needs a commit: an empty one is refused before the gate is
	// reached, which is the point of TestSubmitRefusesAnEmptyChange. This test
	// is about the lock, so give it something real to validate.
	if err := cs.Edit(ctx, "cr-slow", fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"edit", ports.Author{}, "lt-1"); err != nil {
		t.Fatal(err)
	}

	gate.arm()
	done := make(chan error, 1)
	go func() {
		_, err := cs.Submit(ctx, "cr-slow")
		done <- err
	}()

	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the gate was never reached")
	}

	// While that submit is parked in the gate, an unrelated change must still
	// be openable. Open takes the same mutex, so if it were still held this
	// blocks until the gate returns.
	opened := make(chan error, 1)
	go func() {
		_, err := cs.Open(ctx, "cr-other", "another change", submitAuthor)
		opened <- err
	}()
	select {
	case err := <-opened:
		if err != nil {
			t.Fatalf("opening another change while a submit was in the gate: %v", err)
		}
	case <-time.After(5 * time.Second):
		close(gate.release)
		t.Fatal("the service lock is held across the gate: a second change could not be opened while one was building")
	}

	close(gate.release)
	if err := <-done; err != nil {
		t.Fatalf("submit: %v", err)
	}
}

// The flip side: releasing the lock must not let anything mutate the change
// being gated. Nothing needs a tip-moved re-check because the status machine
// already forbids every mutation while a change is Building - this pins that,
// so a later widening of the transition table cannot quietly reintroduce the
// race the lock used to prevent.
func TestChangeBeingGatedCannotBeMutated(t *testing.T) {
	gate := newBlockingGate()
	cs, _, _ := newChangeStackWithGate(t, gate)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "cr-gated", "gated change", submitAuthor); err != nil {
		t.Fatal(err)
	}
	// The change needs a commit: an empty one is refused before the gate is
	// reached, which is the point of TestSubmitRefusesAnEmptyChange. This test
	// is about the lock, so give it something real to validate.
	if err := cs.Edit(ctx, "cr-gated", fleet.SetScopeSetting("device:lt-1", "apps.office", true),
		"edit", ports.Author{}, "lt-1"); err != nil {
		t.Fatal(err)
	}
	gate.arm()
	done := make(chan error, 1)
	go func() {
		_, err := cs.Submit(ctx, "cr-gated")
		done <- err
	}()
	select {
	case <-gate.entered:
	case <-time.After(10 * time.Second):
		t.Fatal("the gate was never reached")
	}
	defer func() {
		close(gate.release)
		if err := <-done; err != nil {
			t.Errorf("submit: %v", err)
		}
	}()

	if err := cs.EditFile(ctx, "cr-gated", "overlays/x.nix", []byte("{}"), "edit", submitAuthor); err == nil {
		t.Error("EditFile accepted an edit while the change was being gated")
	}
	if _, err := cs.Abandon(ctx, "cr-gated"); err == nil {
		t.Error("Abandon succeeded while the change was being gated")
	}
	if _, err := cs.Submit(ctx, "cr-gated"); err == nil {
		t.Error("a second Submit was accepted while the first was still being gated")
	}
	if _, err := cs.Merge(ctx, "cr-gated", ports.Author{Name: "other", Subject: "other"}); err == nil {
		t.Error("Merge succeeded on a change that had not passed the gate")
	}
}
