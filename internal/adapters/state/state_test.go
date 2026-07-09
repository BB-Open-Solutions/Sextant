package state

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

var t0 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

func TestChangeStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	cs := st.Changes()
	ctx := context.Background()

	if _, ok, _ := cs.Get(ctx, "nope"); ok {
		t.Fatal("phantom record")
	}
	a, _ := change.New("first", "t1", "ada", t0)
	b, _ := change.New("second", "t2", "bob", t0.Add(time.Hour))
	if err := cs.Put(ctx, a); err != nil {
		t.Fatal(err)
	}
	if err := cs.Put(ctx, b); err != nil {
		t.Fatal(err)
	}

	got, ok, err := cs.Get(ctx, "first")
	if err != nil || !ok || got.Title != "t1" {
		t.Fatalf("get = %+v %v %v", got, ok, err)
	}

	// List newest first.
	list, err := cs.List(ctx)
	if err != nil || len(list) != 2 || list[0].ID != "second" {
		t.Fatalf("list = %+v, %v", list, err)
	}

	// Durability: a fresh store instance over the same dir sees everything.
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	list2, err := st2.Changes().List(ctx)
	if err != nil || len(list2) != 2 {
		t.Fatalf("rehydrated list = %+v, %v", list2, err)
	}

	// Unsafe ids never touch the filesystem.
	if err := cs.Put(ctx, change.CR{ID: "../escape"}); err == nil {
		t.Fatal("unsafe id accepted")
	}
	if _, _, err := cs.Get(ctx, "../escape"); err == nil {
		t.Fatal("unsafe id read accepted")
	}
}

func TestRolloutStoreRoundTrip(t *testing.T) {
	st, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	rs := st.Rollouts()
	ctx := context.Background()

	got, err := rs.Get(ctx)
	if err != nil || got != nil {
		t.Fatalf("empty get = %+v, %v", got, err)
	}
	s := rollout.NewState("rev-9", t0)
	s.PromotedAt[0] = t0
	if err := rs.Put(ctx, s); err != nil {
		t.Fatal(err)
	}
	got, err = rs.Get(ctx)
	if err != nil || got == nil || got.Target != "rev-9" || got.PromotedAt[0].IsZero() {
		t.Fatalf("get = %+v, %v", got, err)
	}
}
