package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// manyStatus serves a whole observed plane, including rows for devices the
// config plane no longer has.
type manyStatus struct{ sts []observed.DeviceStatus }

func (m *manyStatus) Upsert(context.Context, string, observed.CheckIn, time.Time) (bool, error) {
	return false, nil
}

func (m *manyStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	for _, st := range m.sts {
		if st.Tag == tag {
			return st, true, nil
		}
	}
	return observed.DeviceStatus{}, false, nil
}

func (m *manyStatus) List(context.Context, string) ([]observed.DeviceStatus, error) {
	return m.sts, nil
}
func (m *manyStatus) Ping(context.Context) error { return nil }

func consoleWithGhostCheckIns(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": seedFleet,
		"catalog.json": seedCatalog, "profiles.json": seedProfiles,
		"bundles.json": seedBundles} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	// lt-1 is in the seed fleet. test9 and test10 are not: they were enrolled
	// once, checked in, and were removed from fleet.json afterwards. Their
	// check-in history stays - it is audit material - and that is exactly the
	// state that put deleted machines back on the dashboard.
	store := &manyStatus{sts: []observed.DeviceStatus{
		{Tag: "lt-1", Revision: "abc123", Phase: observed.Running, LastSeen: now},
		{Tag: "test9", Revision: "old999", Phase: observed.Running, LastSeen: now.Add(-72 * time.Hour)},
		{Tag: "test10", Revision: "old111", Phase: observed.Running, LastSeen: now},
	}}
	inv := app.NewInventoryService(store, noFacts{}, fixedClock{now}, app.DefaultTenant)
	srv, err := web.New(web.Services{Config: cfg, Inventory: inv}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestOverviewDropsDevicesTheFleetNoLongerHas: found on the production console,
// 2026-08-05. The devices card read 2 while recent activity listed eight
// machines, six of which had been removed from the fleet weeks earlier. The
// overview was the one surface that walked the observed plane and let anything
// with a check-in through; every other surface joins from f.Devices outward.
//
// A dashboard that contradicts the inventory two rows below it teaches an
// operator to trust neither.
func TestOverviewDropsDevicesTheFleetNoLongerHas(t *testing.T) {
	ts := consoleWithGhostCheckIns(t)
	resp, err := client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("overview = %d\n%s", resp.StatusCode, body)
	}
	s := string(body)
	if !strings.Contains(s, "lt-1") {
		t.Error("the device that still exists is missing from the overview")
	}
	for _, ghost := range []string{"test9", "test10"} {
		if strings.Contains(s, ghost) {
			t.Errorf("%s was removed from the fleet but still appears on the overview", ghost)
		}
	}
	// test10's check-in is fresh, so before the fix it also counted toward
	// "online" - a dashboard could report more machines online than it had.
	if strings.Contains(s, ">2</div>") && strings.Contains(s, "2 online") {
		t.Error("a removed device is still being counted as online")
	}
}
