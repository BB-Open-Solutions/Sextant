package app

import (
	"context"
	"errors"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
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

// TestReconcileSweepsAnOrphanedBranch: found on the production console,
// 2026-08-05. Change cfg-device-dawo-inspoelstraat-10 was abandoned on 17 July
// and nineteen days later its branch and worktree were still in the repository.
// Abandon has called cleanup since the change flow was written, so the cleanup
// ran and failed - and both its errors were discarded, so nothing recorded why.
//
// An abandoned change that still owns a branch is the "orphan in the list" the
// acceptance plan tests for at A7.6, and it is also a branch somebody can still
// merge by hand.
func TestReconcileSweepsAnOrphanedBranch(t *testing.T) {
	cs, _, dir := newChangeStack(t, nil)
	ctx := context.Background()
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}

	if _, err := cs.Open(ctx, "cr-orphan", "left behind", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Abandon(ctx, "cr-orphan"); err != nil {
		t.Fatal(err)
	}
	// Recreate the branch: the state a failed cleanup leaves behind, which is
	// what production was actually in.
	if err := repo.CreateBranch(ctx, "cr/cr-orphan"); err != nil {
		t.Fatal(err)
	}
	if _, err := repo.BranchMerged(ctx, "cr/cr-orphan"); err != nil {
		t.Fatalf("the orphan branch was not set up: %v", err)
	}

	if err := cs.Reconcile(ctx); err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if _, err := repo.BranchMerged(ctx, "cr/cr-orphan"); err == nil {
		t.Fatal("an abandoned change still owns its branch after reconcile")
	}
	// And the change itself is untouched: sweeping leftovers is not a reason
	// to rewrite history.
	got, _, err := cs.Get(ctx, "cr-orphan")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != change.Abandoned {
		t.Fatalf("status = %s, want abandoned", got.Status)
	}
}

// TestReconcileLeavesSettledChangesWithoutBranchesAlone: the sweep must be a
// no-op in the ordinary case, which is every settled change in a healthy
// repository. Reconcile runs at every startup.
func TestReconcileLeavesSettledChangesWithoutBranchesAlone(t *testing.T) {
	cs, _, _ := newChangeStack(t, nil)
	ctx := context.Background()
	if _, err := cs.Open(ctx, "cr-tidy", "cleanly abandoned", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Abandon(ctx, "cr-tidy"); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := cs.Reconcile(ctx); err != nil {
			t.Fatalf("reconcile over a tidy store: %v", err)
		}
	}
	got, _, err := cs.Get(ctx, "cr-tidy")
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != change.Abandoned {
		t.Fatalf("status = %s", got.Status)
	}
}

// TestMergeAndAbandonLeaveNoBranchBehind: assert the EFFECT, not the record.
//
// This is the test that was missing. The change flow's own comment says "the
// git branch is the change itself", and every test around it checked only the
// recorded status - so a cleanup that silently failed left an orphan branch in
// production for nineteen days without a single red test. A contract with a
// side effect outside the store has to be measured outside the store.
func TestMergeAndAbandonLeaveNoBranchBehind(t *testing.T) {
	cs, _, dir := newChangeStack(t, nil)
	ctx := context.Background()
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	gone := func(branch string) bool {
		_, err := repo.BranchMerged(ctx, branch)
		return err != nil
	}

	// Abandon: opened, edited (so it really has a worktree), then dropped.
	if _, err := cs.Open(ctx, "cr-drop", "dropped", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "cr-drop", fleet.SetScopeSetting("org", "dawo.office.enable", true),
		"settings: office on", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if gone("cr/cr-drop") {
		t.Fatal("the branch was never there; this test would prove nothing")
	}
	if _, err := cs.Abandon(ctx, "cr-drop"); err != nil {
		t.Fatal(err)
	}
	if !gone("cr/cr-drop") {
		t.Error("abandon left its branch behind")
	}

	// Merge: the same promise on the other exit.
	if _, err := cs.Open(ctx, "cr-land", "landed", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if err := cs.Edit(ctx, "cr-land", fleet.SetScopeSetting("org", "dawo.office.enable", false),
		"settings: office off", submitAuthor); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Submit(ctx, "cr-land"); err != nil {
		t.Fatal(err)
	}
	if _, err := cs.Merge(ctx, "cr-land", ports.Author{Name: "approver", Subject: "s-approver"}); err != nil {
		t.Fatal(err)
	}
	if !gone("cr/cr-land") {
		t.Error("merge left its branch behind")
	}
}
