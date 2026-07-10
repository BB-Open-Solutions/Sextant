package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

const seedFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
}`

func sh(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// newService builds a ConfigService over a real temp git repo with a seeded
// fleet.json. gate nil means allow-all.
func newService(t *testing.T, gate ports.Gate) (*ConfigService, string) {
	t.Helper()
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if gate == nil {
		gate = ports.GateFunc(func(context.Context, string, []string) error { return nil })
	}
	svc, err := NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestApplyCommitsAndRefreshesSnapshot(t *testing.T) {
	svc, dir := newService(t, nil)

	err := svc.Apply(context.Background(),
		fleet.SetScopeSetting("group:pilot", "apps.office", true),
		"settings: office on for pilot",
		ports.Author{Name: "Ada", Email: "ada@x"}, "lt-1")
	if err != nil {
		t.Fatal(err)
	}

	// Snapshot reflects the write without re-reading disk.
	r := svc.Fleet().Resolve("lt-1")
	if r["apps.office"].Value != true {
		t.Fatalf("snapshot not refreshed: %+v", r["apps.office"])
	}
	// The commit exists with attribution.
	if got := sh(t, dir, "log", "-1", "--format=%an %s"); got != "Ada settings: office on for pilot" {
		t.Fatalf("log = %q", got)
	}
	if got := sh(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("working tree dirty after apply: %q", got)
	}
}

func TestGateRejectionRollsBack(t *testing.T) {
	rejecting := ports.GateFunc(func(context.Context, string, []string) error {
		return &ports.ValidationError{Detail: "unknown setting key"}
	})
	svc, dir := newService(t, rejecting)
	head := sh(t, dir, "rev-parse", "HEAD")

	err := svc.Apply(context.Background(),
		fleet.SetScopeSetting("org", "apps.bogus", true), "bad edit", ports.Author{})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	// No commit, clean working tree, snapshot unchanged.
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("a rejected edit was committed")
	}
	if got := sh(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("working tree dirty after rollback: %q", got)
	}
	if _, has := svc.Fleet().Resolve("lt-1")["apps.bogus"]; has {
		t.Fatal("rejected edit leaked into the snapshot")
	}
}

func TestMutationErrorAborts(t *testing.T) {
	svc, dir := newService(t, nil)
	head := sh(t, dir, "rev-parse", "HEAD")
	err := svc.Apply(context.Background(),
		fleet.SetScopeSetting("group:ghost", "x", 1), "bad scope", ports.Author{})
	if err == nil {
		t.Fatal("want error")
	}
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("mutation error still committed")
	}
	if got := sh(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("working tree dirty: %q", got)
	}
}

// TestConcurrentWritesSerialized is the regression test for the PoC's
// unlocked write path: N concurrent writers must produce exactly N commits
// with no lost updates and no torn files.
func TestConcurrentWritesSerialized(t *testing.T) {
	svc, dir := newService(t, nil)
	base, _ := strconv.Atoi(sh(t, dir, "rev-list", "--count", "HEAD"))

	const n = 12
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = svc.Apply(context.Background(),
				fleet.SetScopeSetting("org", fmt.Sprintf("key.%d", i), i),
				fmt.Sprintf("edit %d", i), ports.Author{})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("writer %d: %v", i, err)
		}
	}
	count, _ := strconv.Atoi(sh(t, dir, "rev-list", "--count", "HEAD"))
	if count != base+n {
		t.Fatalf("commits = %d, want %d (lost or duplicated writes)", count-base, n)
	}
	// Every key survived: no lost updates.
	f := svc.Fleet()
	for i := 0; i < n; i++ {
		if _, has := f.Org.Settings[fmt.Sprintf("key.%d", i)]; !has {
			t.Fatalf("key.%d lost", i)
		}
	}
}

// TestHARetryOnPushRace: with a remote, a lost push race re-applies the
// mutation on the fresh base and retries, keeping both writers' edits.
func TestHARetryOnPushRace(t *testing.T) {
	svc, dir := newService(t, nil)

	// Wire a bare remote after seeding.
	bare := filepath.Join(t.TempDir(), "remote.git")
	sh(t, dir, "clone", "-q", "--bare", dir, bare)
	sh(t, dir, "remote", "add", "origin", bare)
	sh(t, dir, "push", "-q", "origin", "main")
	repo, err := git.Open(dir, "origin")
	if err != nil {
		t.Fatal(err)
	}
	svc2, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	_ = svc

	// A competing clone pushes first.
	other := t.TempDir()
	sh(t, other, "clone", "-q", bare, other)
	otherFleet := strings.Replace(seedFleet, `"desktop": "plasma"`, `"desktop": "gnome"`, 1)
	if err := os.WriteFile(filepath.Join(other, "fleet.json"), []byte(otherFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, other, "add", "fleet.json")
	sh(t, other, "-c", "user.name=o", "-c", "user.email=o@o", "commit", "-q", "-m", "competing edit")
	sh(t, other, "push", "-q", "origin", "main")

	// Our apply must survive the race: re-sync, re-apply, push.
	if err := svc2.Apply(context.Background(),
		fleet.SetScopeSetting("org", "apps.office", true), "our edit", ports.Author{}); err != nil {
		t.Fatal(err)
	}

	// Both edits present on the remote head.
	final := sh(t, dir, "show", "origin/main:fleet.json")
	if !strings.Contains(final, `"desktop": "gnome"`) {
		t.Error("competing edit lost")
	}
	if !strings.Contains(final, `"apps.office": true`) {
		t.Error("our edit lost")
	}
}

// TestSyncLoopPicksUpExternalCommits: the remote is the source of truth -
// a commit pushed by someone else must appear in the snapshot without any
// console write.
func TestSyncLoopPicksUpExternalCommits(t *testing.T) {
	svc, dir := newService(t, nil)
	bare := filepath.Join(t.TempDir(), "remote.git")
	sh(t, dir, "clone", "-q", "--bare", dir, bare)
	sh(t, dir, "remote", "add", "origin", bare)
	sh(t, dir, "push", "-q", "origin", "main")
	repo, err := git.Open(dir, "origin")
	if err != nil {
		t.Fatal(err)
	}
	svc2, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	_ = svc

	// An engineer pushes an external edit.
	other := t.TempDir()
	sh(t, other, "clone", "-q", bare, other)
	ext := strings.Replace(seedFleet, `"desktop": "plasma"`, `"desktop": "gnome"`, 1)
	if err := os.WriteFile(filepath.Join(other, "fleet.json"), []byte(ext), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, other, "add", "fleet.json")
	sh(t, other, "-c", "user.name=e", "-c", "user.email=e@e", "commit", "-q", "-m", "external edit")
	sh(t, other, "push", "-q", "origin", "main")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go svc2.SyncLoop(ctx, 30*time.Millisecond, slog.New(slog.NewTextHandler(io.Discard, nil)))

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if svc2.Fleet().Org.Settings["desktop"] == "gnome" {
			return // snapshot refreshed from the remote
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("external commit never reached the snapshot")
}
