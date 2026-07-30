package app

import (
	"context"
	"errors"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// dropMergedPut is the failure this repairs: the merge lands in git, and then
// persisting the Merged status fails. Nothing else about Merge is disturbed -
// in particular its cleanup never runs, so the branch is still there for git to
// answer about, which is exactly the state the console would restart into.
type dropMergedPut struct {
	ports.ChangeStore
	dropped bool
}

func (d *dropMergedPut) Put(ctx context.Context, cr change.CR) error {
	if cr.Status == change.Merged && !d.dropped {
		d.dropped = true
		return errors.New("simulated store failure while recording the merge")
	}
	return d.ChangeStore.Put(ctx, cr)
}

// Merge lands the merge in git BEFORE recording it, deliberately: once
// MergeNoFF succeeds the merge is irreversible, so this ordering means the
// database can never claim a merge that did not happen. The price is the
// opposite gap - a merge that DID happen while persisting the status failed
// leaves the change in Ready over a main branch that already contains it. An
// approver then sees a change awaiting approval it has already had, and
// re-merging fails with "Already up to date".
//
// Reconcile closes that gap by asking git, which is the source of truth for
// configuration everywhere else in this design.
func TestReconcileMarksAMergedChangeMerged(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()
	store := &dropMergedPut{ChangeStore: cs.store}
	cs.store = store

	if _, err := cs.Open(ctx, "cr-r1", "reconcile me", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if err := cs.EditFile(ctx, "cr-r1", "overlays/r1.nix", []byte("{ }\n"), "add r1", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, "cr-r1"); err != nil {
		t.Fatal(err)
	}
	approver := ports.Author{Name: "bob", Subject: "bob-subject"}
	if _, err := cs.Merge(ctx, "cr-r1", approver); err == nil {
		t.Fatal("the simulated store failure did not surface; the test is not reproducing the gap")
	}
	if !store.dropped {
		t.Fatal("the Merged status write was never attempted")
	}
	// Git holds the merge; the record does not. This is what a restart sees.
	if before, _, err := cs.Get(ctx, "cr-r1"); err != nil {
		t.Fatal(err)
	} else if before.Status != change.Ready {
		t.Fatalf("precondition: status is %s, want ready", before.Status)
	}

	if err := cs.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, ok, err := cs.Get(ctx, "cr-r1")
	if err != nil || !ok {
		t.Fatalf("get after reconcile: %v (found %v)", err, ok)
	}
	if got.Status != change.Merged {
		t.Fatalf("status is %s; a change already merged in git must be recorded as merged", got.Status)
	}
}

// Reconcile must not touch a change that is genuinely waiting for approval.
// Marking an unmerged change merged would make it disappear from the review
// queue while its content never reached main - a silently dropped change is
// worse than a confusing one.
func TestReconcileLeavesAnUnmergedReadyChangeAlone(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "cr-r2", "still waiting", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if err := cs.EditFile(ctx, "cr-r2", "overlays/r2.nix", []byte("{ }\n"), "add r2", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, "cr-r2"); err != nil {
		t.Fatal(err)
	}

	if err := cs.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	got, _, err := cs.Get(ctx, "cr-r2")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != change.Ready {
		t.Fatalf("status is %s; an unmerged change must stay ready for review", got.Status)
	}
}

// Statuses other than Ready are not the gap being repaired, and a
// reconciliation that reached into them could resurrect an abandoned change or
// finish a draft nobody approved.
func TestReconcileIgnoresOtherStatuses(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()

	if _, err := cs.Open(ctx, "cr-r3", "draft", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Open(ctx, "cr-r4", "abandoned", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Abandon(ctx, "cr-r4"); err != nil {
		t.Fatal(err)
	}

	if err := cs.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	for id, want := range map[string]change.Status{
		"cr-r3": change.Draft,
		"cr-r4": change.Abandoned,
	} {
		got, _, err := cs.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != want {
			t.Errorf("%s: status is %s, want %s", id, got.Status, want)
		}
	}
}

// Reconcile runs at startup, so it must be idempotent: a second pass over an
// already-corrected store has nothing to do and must not error.
func TestReconcileIsIdempotent(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()
	for range 3 {
		if err := cs.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile on an empty store: %v", err)
		}
	}
}
