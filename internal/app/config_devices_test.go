package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// The reaper deletes devices, so what it must never do matters more than what
// it does. Four records, and only one of them is a legitimate target:
//
//	stale-provisional  provisional, enrolled long ago      -> reap
//	fresh-provisional  provisional, enrolled minutes ago   -> keep
//	stale-active       reported once, so not provisional   -> keep
//	unstamped          provisional, no enrolment date      -> keep
//
// The last two are the ones a careless rewrite loses. A sweep that keyed on
// age alone would take stale-active, which is a machine somebody is using; one
// that treated a missing date as ancient would take unstamped, which is a
// record that predates the field.
const reapSeed = `{
  "version": 3,
  "groups": {"pilot": {}},
  "devices": {
    "stale-provisional": {"groups": ["pilot"], "state": "provisional", "enrolled": "2026-01-01T00:00:00Z"},
    "fresh-provisional": {"groups": ["pilot"], "state": "provisional", "enrolled": "2099-01-01T00:00:00Z"},
    "stale-active":      {"groups": ["pilot"], "enrolled": "2026-01-01T00:00:00Z"},
    "unstamped":         {"groups": ["pilot"], "state": "provisional"}
  }
}`

// reapService is newService with a fleet built for this file. Kept separate
// rather than parameterising newService: seedFleet is read by a dozen tests
// and widening it to suit one is how a shared fixture becomes unreadable.
func reapService(t *testing.T) (*ConfigService, string) {
	t.Helper()
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(reapSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	svc, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	return svc, dir
}

func TestReapAbandonedEnrolmentsTakesOnlyTheAbandonedOnes(t *testing.T) {
	svc, dir := reapService(t)
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)

	got, err := svc.ReapAbandonedEnrolments(context.Background(), now, ports.Author{Name: "sweep"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "stale-provisional" {
		t.Fatalf("reaped %v, want [stale-provisional]", got)
	}

	f := svc.Fleet()
	for _, tag := range []string{"fresh-provisional", "stale-active", "unstamped"} {
		if _, ok := f.Devices[tag]; !ok {
			t.Errorf("%s was deleted and should not have been", tag)
		}
	}
	if _, ok := f.Devices["stale-provisional"]; ok {
		t.Error("stale-provisional survived the sweep")
	}

	// One commit for the whole sweep, and a subject an operator can read
	// without opening the diff.
	msg := sh(t, dir, "log", "-1", "--format=%s")
	if !strings.Contains(msg, "stale-provisional") {
		t.Errorf("commit subject does not name what it removed: %q", msg)
	}
	if !strings.Contains(msg, "1 enrolment") {
		t.Errorf("commit subject does not count what it removed: %q", msg)
	}
}

// The cutoff is 72 hours (fleet.StaleProvisional), and it is a boundary a
// change to the constant must not cross silently. Enrolled exactly at the
// cutoff is NOT abandoned: AbandonedEnrolments uses Before, not !After.
func TestReapRespectsTheCutoffBoundary(t *testing.T) {
	svc, _ := reapService(t)
	enrolled := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)

	atCutoff := enrolled.Add(fleet.StaleProvisional)
	got, err := svc.ReapAbandonedEnrolments(context.Background(), atCutoff, ports.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("reaped %v at exactly the cutoff; the record is 72h old, not older", got)
	}

	got, err = svc.ReapAbandonedEnrolments(context.Background(),
		atCutoff.Add(time.Nanosecond), ports.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("reaped %v one nanosecond past the cutoff, want one", got)
	}
}

// Nothing stale must write nothing at all. A sweep that runs on every rollout
// tick and commits each time would bury the history it shares with real
// changes, and the early return is the only thing preventing that.
func TestReapWithNothingStaleWritesNoCommit(t *testing.T) {
	svc, dir := reapService(t)
	before := sh(t, dir, "rev-parse", "HEAD")

	// Before any enrolment aged out.
	got, err := svc.ReapAbandonedEnrolments(context.Background(),
		time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC), ports.Author{})
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Errorf("reaped %v with nothing stale", got)
	}
	if after := sh(t, dir, "rev-parse", "HEAD"); after != before {
		t.Error("an empty sweep still committed")
	}
}

// The re-check inside the mutation, which is the whole reason the sweep reads
// the fleet twice. Driven for real: a competing writer activates the device on
// the remote after we have listed it as stale and before our write lands, which
// is exactly the race a machine finishing its install at that moment produces.
//
// Without the re-check this deletes a device that had just reported in - the
// worst outcome this code has, because the machine is alive and now unknown.
func TestReapSkipsADeviceThatReportedDuringTheSweep(t *testing.T) {
	svc, dir := reapService(t)

	bare := filepath.Join(t.TempDir(), "remote.git")
	sh(t, dir, "clone", "-q", "--bare", dir, bare)
	sh(t, dir, "remote", "add", "origin", bare)
	sh(t, dir, "push", "-q", "origin", "main")
	repo, err := git.Open(dir, "origin")
	if err != nil {
		t.Fatal(err)
	}
	// Built from the pre-race clone, so its snapshot still says provisional.
	racing, err := NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	if !racing.Fleet().Devices["stale-provisional"].Provisional() {
		t.Fatal("fixture: the racing service should still see a provisional device")
	}
	_ = svc

	// The device reports in. Somebody else's console writes the activation and
	// pushes it while our sweep is holding a stale list.
	other := t.TempDir()
	sh(t, other, "clone", "-q", bare, other)
	activated := strings.Replace(reapSeed,
		`"stale-provisional": {"groups": ["pilot"], "state": "provisional", "enrolled": "2026-01-01T00:00:00Z"}`,
		`"stale-provisional": {"groups": ["pilot"], "enrolled": "2026-01-01T00:00:00Z"}`, 1)
	if activated == reapSeed {
		t.Fatal("fixture: the activation edit did not apply")
	}
	if err := os.WriteFile(filepath.Join(other, "fleet.json"), []byte(activated), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, other, "add", "fleet.json")
	sh(t, other, "-c", "user.name=o", "-c", "user.email=o@o", "commit", "-q", "-m", "device reported in")
	sh(t, other, "push", "-q", "origin", "main")

	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	if _, err := racing.ReapAbandonedEnrolments(context.Background(), now, ports.Author{Name: "sweep"}); err != nil {
		t.Fatalf("the sweep must not fail on a device that got away: %v", err)
	}

	// The only assertion that matters: the machine still exists.
	final := sh(t, dir, "show", "origin/main:fleet.json")
	if !strings.Contains(final, "stale-provisional") {
		t.Fatal("a device that reported in during the sweep was deleted anyway")
	}
}
