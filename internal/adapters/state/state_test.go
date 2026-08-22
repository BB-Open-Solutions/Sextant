package state

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	a, _ := change.New("first", "t1", "ada", "sub", t0)
	b, _ := change.New("second", "t2", "bob", "sub", t0.Add(time.Hour))
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

// The upstream store is the watcher's whole memory: it holds the last core
// revision already staged as a change request. Forgetting it re-stages the
// same core update on every tick; remembering the wrong thing means a real
// core update never gets offered. Both are silent, and neither is visible
// until somebody wonders why the update board looks the way it does.
func TestUpstreamStoreRoundTripSurvivesAReopen(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()

	// Nothing staged yet. First boot must be an empty answer rather than an
	// error, or the watcher fails on the one run where it has most to do.
	got, err := st.Upstream().LastUpstream(ctx)
	if err != nil {
		t.Fatalf("reading an absent upstream.json: %v", err)
	}
	if got != "" {
		t.Errorf("empty store returned %q", got)
	}

	const rev = "6269d0d1f3a04c9b8e2d5a71c4f80b3e9a6d2c11"
	if err := st.Upstream().PutUpstream(ctx, rev); err != nil {
		t.Fatal(err)
	}

	// Reopened, because persisting it is the entire point: the process this
	// serves is a watcher that restarts.
	st2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err = st2.Upstream().LastUpstream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != rev {
		t.Errorf("after reopen got %q, want %q", got, rev)
	}

	// A newer revision replaces it rather than accumulating.
	const newer = "3eb66714569fce0e4f2938f456e26e3eef494772"
	if err := st2.Upstream().PutUpstream(ctx, newer); err != nil {
		t.Fatal(err)
	}
	got, err = st2.Upstream().LastUpstream(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got != newer {
		t.Errorf("after overwrite got %q, want %q", got, newer)
	}
}

// writeJSON writes to a temp file, fsyncs, renames, then fsyncs the
// directory. That sequence exists so a crash mid-write leaves either the old
// document or the new one, never half of either. What a test can check
// cheaply is the observable half: the temp file does not survive the write,
// and the mode is the restrictive one the code asked for.
func TestWriteLeavesNoTempFileBehind(t *testing.T) {
	dir := t.TempDir()
	st, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Upstream().PutUpstream(context.Background(), "abc123"); err != nil {
		t.Fatal(err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	for _, n := range names {
		if strings.HasSuffix(n, ".tmp") {
			t.Errorf("a temp file survived the write: %v", names)
		}
	}

	fi, err := os.Stat(filepath.Join(dir, "upstream.json"))
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode %o, want 600", perm)
	}
}

// The first thing a container deployment hits: the image runs as its own uid
// and the state directory defaults to <repo>/.sextant-state, so a volume owned
// by the operator stops the server before it logs anything else. "permission
// denied" on its own sent the first person who tried it looking at SELinux and
// the database.
func TestAnUnwritableStateDirSaysWhatToDo(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root writes anywhere; this failure needs an ordinary user")
	}
	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o500); err != nil {
		t.Fatal(err)
	}
	_, err := Open(filepath.Join(locked, "state"))
	if err == nil {
		t.Fatal("an unwritable state dir was accepted")
	}
	msg := err.Error()
	for _, want := range []string{"--state-dir", "ownership", locked} {
		if !strings.Contains(msg, want) {
			t.Errorf("the error does not mention %q: %s", want, msg)
		}
	}
}
