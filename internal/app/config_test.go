package app

import (
	"bytes"
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

// TestNewConfigServiceRejectsInvalidFleet: a repo whose fleet.json does not
// parse must fail construction loudly, not hand back a service over a
// half-loaded snapshot.
func TestNewConfigServiceRejectsInvalidFleet(t *testing.T) {
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil })); err == nil {
		t.Fatal("NewConfigService accepted an unparsable fleet.json")
	}
}

// TestApplyIdempotentNoOp: a mutation that produces the exact same document
// applyTx already holds is a no-op - no gate call, no commit. The very first
// write after seeding always commits (raw seed JSON never byte-matches
// Encode's canonical form), so this checks the SECOND identical write, once
// the working tree already holds the canonical encoding.
func TestApplyIdempotentNoOp(t *testing.T) {
	svc, dir := newService(t, nil)
	mut := fleet.SetScopeSetting("org", "desktop", "plasma") // seedFleet already holds this value
	if err := svc.Apply(context.Background(), mut, "first (canonicalizing) edit", ports.Author{}); err != nil {
		t.Fatalf("first Apply: %v", err)
	}
	head := sh(t, dir, "rev-parse", "HEAD")

	if err := svc.Apply(context.Background(), mut, "second (no-op) edit", ports.Author{}); err != nil {
		t.Fatalf("second Apply: %v", err)
	}
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("an idempotent mutation produced a commit")
	}
}

// TestApplyDecodeErrorOnCorruptedWorkingTree: applyTx's read-decode step must
// surface a corrupted fleet.json (written outside the service, e.g. by a bad
// external merge) as an error rather than panicking or silently overwriting
// it.
func TestApplyDecodeErrorOnCorruptedWorkingTree(t *testing.T) {
	svc, dir := newService(t, nil)
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte("{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=e", "-c", "user.email=e@e", "commit", "-q", "-m", "corrupt")

	err := svc.Apply(context.Background(),
		fleet.SetScopeSetting("org", "desktop", "gnome"), "edit over corruption", ports.Author{})
	if err == nil {
		t.Fatal("Apply over a corrupted fleet.json was accepted")
	}
}

// TestAuditLog exercises the audit-log passthrough over a real git repo,
// which implements ports.AuditLog: newest-first, capped at the limit.
func TestAuditLog(t *testing.T) {
	svc, _ := newService(t, nil)
	for i := 0; i < 3; i++ {
		if err := svc.Apply(context.Background(),
			fleet.SetScopeSetting("org", fmt.Sprintf("k%d", i), i),
			fmt.Sprintf("edit %d", i), ports.Author{Name: "Ada", Email: "ada@x"}); err != nil {
			t.Fatal(err)
		}
	}
	entries, err := svc.AuditLog(context.Background(), 2)
	if err != nil {
		t.Fatalf("AuditLog: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("AuditLog len = %d, want 2 (limit)", len(entries))
	}
	if entries[0].Subject != "edit 2" {
		t.Fatalf("AuditLog not newest-first: %+v", entries)
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

// TestSyncLoopNoRemoteIsNoOp: without a remote, SyncLoop must return
// immediately rather than block on a ticker that will never matter.
func TestSyncLoopNoRemoteIsNoOp(t *testing.T) {
	svc, _ := newService(t, nil)
	done := make(chan struct{})
	go func() {
		svc.SyncLoop(context.Background(), time.Hour, slog.New(slog.NewTextHandler(io.Discard, nil)))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SyncLoop over a repo without a remote did not return immediately")
	}
}

// TestSyncLoopLogsOnSyncFailure: a remote that goes unreachable mid-loop must
// be logged and retried, never crash the loop.
func TestSyncLoopLogsOnSyncFailure(t *testing.T) {
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

	// The remote goes unreachable: every Sync from here on fails.
	if err := os.RemoveAll(bare); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 300*time.Millisecond)
	defer cancel()
	svc2.SyncLoop(ctx, 20*time.Millisecond, slog.New(slog.NewTextHandler(&buf, nil)))

	if !strings.Contains(buf.String(), "remote sync failed") {
		t.Fatalf("SyncLoop did not log the sync failure: %s", buf.String())
	}
}

func TestOverlayWriteListReadDelete(t *testing.T) {
	svc, _ := newService(t, nil) // allow-all gate
	ctx := context.Background()
	code := "{ ... }:\n{\n  environment.systemPackages = [ ];\n}\n"

	if err := svc.WriteOverlay(ctx, "k8s-node", code, ports.Author{Name: "op"}); err != nil {
		t.Fatalf("WriteOverlay: %v", err)
	}
	names, err := svc.ListOverlays()
	if err != nil || len(names) != 1 || names[0] != "k8s-node" {
		t.Fatalf("ListOverlays = %v, %v", names, err)
	}
	got, err := svc.ReadOverlay("k8s-node")
	if err != nil || got != code {
		t.Fatalf("ReadOverlay mismatch: %q", got)
	}
	// A bad name is rejected.
	if err := svc.WriteOverlay(ctx, "Bad Name", code, ports.Author{}); err == nil {
		t.Fatal("accepted a non-slug overlay name")
	}
	if err := svc.DeleteOverlay(ctx, "k8s-node", ports.Author{Name: "op"}); err != nil {
		t.Fatalf("DeleteOverlay: %v", err)
	}
	if names, _ := svc.ListOverlays(); len(names) != 0 {
		t.Fatalf("overlay still present after delete: %v", names)
	}
}

// --- SetSetting / ClearSetting / ApplySettings (catalog-gated writes) ---

// seedCatalogApp mirrors the shape internal/http/web and internal/http/api
// test fixtures use: a toggle, a text setting and a secret-reference setting,
// enough to exercise every ParseValue branch ConfigService's write path
// delegates to.
const seedCatalogApp = `[
  {"name":"apps.office","type":"boolean","description":"Office suite"},
  {"name":"desktop","type":"string","description":"Desktop environment"},
  {"name":"netbird.setupKey","type":"string","description":"NetBird join key","secret":true}
]`

// seedFleetWithSecretRef is seedFleet plus one registered secret reference,
// so a secret-typed setting has a valid target to point at.
const seedFleetWithSecretRef = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}},
  "secretRefs": {"vpn-key": {"description": "NetBird setup key"}}
}`

// seedFleetGoverned is seedFleet with change-request governance turned on, so
// every direct write must be refused.
const seedFleetGoverned = `{
  "version": 3,
  "assurance": {"requireChangeRequest": true},
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
}`

// newCatalogService builds a ConfigService over a temp git repo seeded with
// fleetDoc plus seedCatalogApp, so SetSetting/ClearSetting/ApplySettings can
// resolve catalog entries. The gate is allow-all: every test in this group
// exercises ConfigService's own validation, not the gate's.
func newCatalogService(t *testing.T, fleetDoc string) (*ConfigService, string) {
	t.Helper()
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": fleetDoc, "catalog.json": seedCatalogApp} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	sh(t, dir, "add", ".")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	gate := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	svc, err := NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestSetSettingSetsAndRejectsInvalid(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	// A valid value is applied.
	enforceOn := true
	if err := svc.SetSetting(ctx, "org", "apps.office", "true", &enforceOn, author); err != nil {
		t.Fatalf("SetSetting: %v", err)
	}
	own, enforced, _ := svc.Fleet().ScopeSettings("org")
	if own["apps.office"] != true || len(enforced) != 1 || enforced[0] != "apps.office" {
		t.Fatalf("after set: own=%v enforced=%v", own, enforced)
	}

	// A registered secret reference is accepted.
	if err := svc.SetSetting(ctx, "org", "netbird.setupKey", "vpn-key", nil, author); err != nil {
		t.Fatalf("SetSetting secret ref: %v", err)
	}
	own, _, _ = svc.Fleet().ScopeSettings("org")
	if own["netbird.setupKey"] != "vpn-key" {
		t.Fatalf("secret ref not set: %v", own)
	}

	// An unregistered secret reference is refused.
	if err := svc.SetSetting(ctx, "org", "netbird.setupKey", "ghost", nil, author); err == nil {
		t.Fatal("dangling secret ref accepted")
	}

	// An unknown catalog key is refused.
	if err := svc.SetSetting(ctx, "org", "not.in.catalog", "x", nil, author); err == nil {
		t.Fatal("unknown key accepted")
	}

	// An empty value is refused (clear is a separate call).
	if err := svc.SetSetting(ctx, "org", "desktop", "  ", nil, author); err == nil {
		t.Fatal("blank value accepted")
	}

	// A value the catalog type rejects is refused.
	if err := svc.SetSetting(ctx, "org", "apps.office", "maybe", nil, author); err == nil {
		t.Fatal("mistyped value accepted")
	}
}

func TestClearSettingRevertsToInherited(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)
	ctx := context.Background()
	author := ports.Author{Name: "Ada", Email: "ada@x"}

	if err := svc.ClearSetting(ctx, "org", "desktop", author); err != nil {
		t.Fatalf("ClearSetting: %v", err)
	}
	own, _, _ := svc.Fleet().ScopeSettings("org")
	if _, has := own["desktop"]; has {
		t.Fatalf("desktop still set after clear: %v", own)
	}
}

// TestSetSettingAndApplySettingsRejectUnknownScope: a syntactically valid but
// non-existent scope surfaces the mutation error from fleet.SetScopeSetting,
// through both the single-setting and the batch write path.
func TestSetSettingAndApplySettingsRejectUnknownScope(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)
	ctx := context.Background()

	if err := svc.SetSetting(ctx, "group:ghost", "desktop", "gnome", nil, ports.Author{}); err == nil {
		t.Fatal("SetSetting accepted an unknown group scope")
	}
	if err := svc.ApplySettings(ctx, "group:ghost",
		[]SettingChange{{Key: "desktop", RawValue: "gnome"}}, ports.Author{}); err == nil {
		t.Fatal("ApplySettings accepted an unknown group scope")
	}
}

func TestSetAndClearSettingBlockedUnderGovernance(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetGoverned)
	ctx := context.Background()

	if err := svc.SetSetting(ctx, "org", "desktop", "gnome", nil, ports.Author{}); !errors.Is(err, ErrChangeRequestRequired) {
		t.Fatalf("SetSetting under governance = %v, want ErrChangeRequestRequired", err)
	}
	if err := svc.ClearSetting(ctx, "org", "desktop", ports.Author{}); !errors.Is(err, ErrChangeRequestRequired) {
		t.Fatalf("ClearSetting under governance = %v, want ErrChangeRequestRequired", err)
	}
	if err := svc.ApplySettings(ctx, "org", []SettingChange{{Key: "desktop", RawValue: "gnome"}}, ports.Author{}); !errors.Is(err, ErrChangeRequestRequired) {
		t.Fatalf("ApplySettings under governance = %v, want ErrChangeRequestRequired", err)
	}
}

// TestApplySettingsBatchSingleCommit is the save-all regression: several
// changes at one scope must land in exactly ONE gated commit, not one per
// key.
func TestApplySettingsBatchSingleCommit(t *testing.T) {
	svc, dir := newCatalogService(t, seedFleetWithSecretRef)
	head := sh(t, dir, "rev-list", "--count", "HEAD")

	changes := []SettingChange{
		{Key: "apps.office", RawValue: "true", Enforce: true},
		{Key: "netbird.setupKey", RawValue: "vpn-key"},
	}
	if err := svc.ApplySettings(context.Background(), "org", changes, ports.Author{Name: "Ada", Email: "ada@x"}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}

	newHead := sh(t, dir, "rev-list", "--count", "HEAD")
	n0, _ := strconv.Atoi(head)
	n1, _ := strconv.Atoi(newHead)
	if n1 != n0+1 {
		t.Fatalf("commits = %d, want exactly one new commit (was %d)", n1-n0, n1-n0)
	}

	own, enforced, _ := svc.Fleet().ScopeSettings("org")
	if own["apps.office"] != true || own["netbird.setupKey"] != "vpn-key" {
		t.Fatalf("batch not fully applied: %v", own)
	}
	if len(enforced) != 1 || enforced[0] != "apps.office" {
		t.Fatalf("enforce not applied: %v", enforced)
	}
}

// TestApplySettingsClearInBatch: a Clear change reverts that key to
// inherited even while other changes in the same batch set values.
func TestApplySettingsClearInBatch(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)

	changes := []SettingChange{
		{Key: "desktop", Clear: true},
		{Key: "apps.office", RawValue: "true"},
	}
	if err := svc.ApplySettings(context.Background(), "org", changes, ports.Author{}); err != nil {
		t.Fatalf("ApplySettings: %v", err)
	}
	own, _, _ := svc.Fleet().ScopeSettings("org")
	if _, has := own["desktop"]; has {
		t.Fatalf("desktop still set after batch clear: %v", own)
	}
	if own["apps.office"] != true {
		t.Fatalf("apps.office not set: %v", own)
	}
}

// TestApplySettingsRejectsUnknownKey: an unknown catalog key in the batch
// rejects the whole save.
func TestApplySettingsRejectsUnknownKey(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)
	changes := []SettingChange{
		{Key: "apps.office", RawValue: "true"},
		{Key: "not.in.catalog", RawValue: "x"},
	}
	if err := svc.ApplySettings(context.Background(), "org", changes, ports.Author{}); err == nil {
		t.Fatal("unknown key in batch accepted")
	}
	own, _, _ := svc.Fleet().ScopeSettings("org")
	if _, has := own["apps.office"]; has {
		t.Fatalf("valid change in a rejected batch was applied: %v", own)
	}
}

// TestApplySettingsRejectsDanglingSecretRef: an unregistered secret
// reference anywhere in the batch rejects the whole save.
func TestApplySettingsRejectsDanglingSecretRef(t *testing.T) {
	svc, _ := newCatalogService(t, seedFleetWithSecretRef)
	changes := []SettingChange{
		{Key: "apps.office", RawValue: "true"},
		{Key: "netbird.setupKey", RawValue: "ghost-ref"},
	}
	if err := svc.ApplySettings(context.Background(), "org", changes, ports.Author{}); err == nil {
		t.Fatal("dangling secret ref in batch accepted")
	}
	own, _, _ := svc.Fleet().ScopeSettings("org")
	if _, has := own["apps.office"]; has {
		t.Fatalf("valid change in a rejected batch was applied: %v", own)
	}
}

// TestApplySettingsValidatesAllBeforeMutating is the core "all or nothing"
// invariant: a bad value LATER in the batch must not leave EARLIER changes
// applied, because validation runs over the whole batch before any mutation
// touches the working document.
func TestApplySettingsValidatesAllBeforeMutating(t *testing.T) {
	svc, dir := newCatalogService(t, seedFleetWithSecretRef)
	head := sh(t, dir, "rev-parse", "HEAD")

	changes := []SettingChange{
		{Key: "apps.office", RawValue: "true"},
		{Key: "desktop", RawValue: "gnome"},
		{Key: "apps.office", RawValue: "maybe"}, // fails ParseValue: not a bool
	}
	if err := svc.ApplySettings(context.Background(), "org", changes, ports.Author{}); err == nil {
		t.Fatal("batch with a bad value in it was accepted")
	}

	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("a rejected batch was committed")
	}
	own, _, _ := svc.Fleet().ScopeSettings("org")
	if own["desktop"] != "plasma" {
		t.Fatalf("earlier valid change in a rejected batch leaked: desktop=%v", own["desktop"])
	}
	if _, has := own["apps.office"]; has {
		t.Fatalf("earlier valid change in a rejected batch leaked: %v", own)
	}
}

// TestApplySettingsEmptyIsNoOp: an empty batch commits nothing.
func TestApplySettingsEmptyIsNoOp(t *testing.T) {
	svc, dir := newCatalogService(t, seedFleetWithSecretRef)
	head := sh(t, dir, "rev-parse", "HEAD")
	if err := svc.ApplySettings(context.Background(), "org", nil, ports.Author{}); err != nil {
		t.Fatalf("ApplySettings(nil): %v", err)
	}
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("empty batch produced a commit")
	}
}

// TestReadAccessors covers the snapshot read surface (Head, Catalog,
// HardwareProfiles, Snapshot) that ApplySettings and the transports rely on
// but had no direct test: each must reflect the same seeded revision.
func TestReadAccessors(t *testing.T) {
	svc, dir := newCatalogService(t, seedFleetWithSecretRef)

	if got, want := svc.Head(context.Background()), sh(t, dir, "rev-parse", "HEAD"); got != want {
		t.Fatalf("Head = %q, want %q", got, want)
	}
	if cat := svc.Catalog(); cat == nil || len(cat.Entries) != 3 {
		t.Fatalf("Catalog = %v, want the 3 seeded entries", cat)
	}
	if hw := svc.HardwareProfiles(); hw == nil {
		t.Fatal("HardwareProfiles = nil, want the empty-but-non-nil catalog")
	}
	f, cat := svc.Snapshot()
	if f != svc.Fleet() || cat != svc.Catalog() {
		t.Fatal("Snapshot did not return the same fleet/catalog pointers as Fleet()/Catalog()")
	}
}

// TestReload picks up an edit made directly on disk (bypassing Apply), the
// same way a change request merged behind the service's back would.
func TestReload(t *testing.T) {
	svc, dir := newCatalogService(t, seedFleetWithSecretRef)

	edited := strings.Replace(seedFleetWithSecretRef, `"desktop": "plasma"`, `"desktop": "gnome"`, 1)
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(edited), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=e", "-c", "user.email=e@e", "commit", "-q", "-m", "external edit")

	if err := svc.Reload(); err != nil {
		t.Fatalf("Reload: %v", err)
	}
	if svc.Fleet().Org.Settings["desktop"] != "gnome" {
		t.Fatalf("Reload did not pick up the external edit: %v", svc.Fleet().Org.Settings)
	}
}

// TestOverlayReadDeleteRejectBadNames covers the name-slug guard on the two
// remaining overlay entry points that TestOverlayWriteListReadDelete does not
// exercise directly (it only checks WriteOverlay's copy of the same guard).
func TestOverlayReadDeleteRejectBadNames(t *testing.T) {
	svc, _ := newService(t, nil)
	if _, err := svc.ReadOverlay("Bad Name"); err == nil {
		t.Fatal("ReadOverlay accepted a non-slug name")
	}
	if err := svc.DeleteOverlay(context.Background(), "Bad Name", ports.Author{}); err == nil {
		t.Fatal("DeleteOverlay accepted a non-slug name")
	}
}

// TestOverlayWriteIdempotentNoOp: writing the exact same content twice must
// not produce a second commit - auxOnce's idempotent no-op path.
func TestOverlayWriteIdempotentNoOp(t *testing.T) {
	svc, dir := newService(t, nil)
	ctx := context.Background()
	code := "{ ... }:\n{\n  environment.systemPackages = [ ];\n}\n"

	if err := svc.WriteOverlay(ctx, "k8s-node", code, ports.Author{Name: "op"}); err != nil {
		t.Fatalf("first WriteOverlay: %v", err)
	}
	head := sh(t, dir, "rev-parse", "HEAD")

	if err := svc.WriteOverlay(ctx, "k8s-node", code, ports.Author{Name: "op"}); err != nil {
		t.Fatalf("second (idempotent) WriteOverlay: %v", err)
	}
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("an identical rewrite produced a new commit")
	}
}

// TestDeleteOverlayAlreadyGoneIsNoOp: deleting a name that was never written
// (or already removed) reports success - the desired state already holds.
func TestDeleteOverlayAlreadyGoneIsNoOp(t *testing.T) {
	svc, dir := newService(t, nil)
	head := sh(t, dir, "rev-parse", "HEAD")
	if err := svc.DeleteOverlay(context.Background(), "never-existed", ports.Author{}); err != nil {
		t.Fatalf("DeleteOverlay on an absent module: %v", err)
	}
	if got := sh(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatal("deleting an absent overlay produced a commit")
	}
}

// TestOverlayHARetryOnPushRace is auxApply's version of
// TestHARetryOnPushRace: a lost push race on an overlay write must re-sync
// and retry rather than fail or clobber the competing commit.
func TestOverlayHARetryOnPushRace(t *testing.T) {
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

	// A competing clone pushes a change first.
	other := t.TempDir()
	sh(t, other, "clone", "-q", bare, other)
	sh(t, other, "-c", "user.name=o", "-c", "user.email=o@o", "commit", "--allow-empty", "-q", "-m", "competing edit")
	sh(t, other, "push", "-q", "origin", "main")

	code := "{ ... }:\n{\n  environment.systemPackages = [ ];\n}\n"
	if err := svc2.WriteOverlay(context.Background(), "k8s-node", code, ports.Author{Name: "op"}); err != nil {
		t.Fatalf("WriteOverlay under a push race: %v", err)
	}

	final := sh(t, dir, "log", "origin/main", "--format=%s")
	if !strings.Contains(final, "competing edit") {
		t.Error("competing edit lost")
	}
	if !strings.Contains(final, "overlays: write k8s-node") {
		t.Error("our overlay write lost")
	}
}

func TestOverlayWriteRejectedRollsBack(t *testing.T) {
	reject := ports.GateFunc(func(context.Context, string, []string) error {
		return fmt.Errorf("does not evaluate")
	})
	svc, _ := newService(t, reject)
	err := svc.WriteOverlay(context.Background(), "broken", "{ this is not nix", ports.Author{})
	if err == nil {
		t.Fatal("rejected overlay was accepted")
	}
	// The rejected new file must not linger (rollback removed it).
	if names, _ := svc.ListOverlays(); len(names) != 0 {
		t.Fatalf("rejected overlay left behind: %v", names)
	}
}
