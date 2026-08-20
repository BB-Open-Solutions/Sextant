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

// The render smoke net runs every page against a fleet with one device, one
// group and no policies at all. That is not a small fleet, it is a fleet with
// every data-carrying branch switched off: `range .Policies` has nothing to
// loop over, a device row is never built, a rollout plan is never drawn. On
// 2026-07-30 the overview shipped a nil-map index that no test could reach for
// exactly this reason, and it broke the first page an operator sees.
//
// So this is the same sweep against a fleet that has things in it. Not a
// realistic fleet - a fleet where every list has at least one entry, which is
// the cheapest way to make the loop bodies execute.

const populatedFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma", "apps.office": true, "netbird.enable": true}},
  "assurance": {"requireFourEyes": true},
  "groups": {
    "pilot": {"settings": {"desktop": "gnome"}},
    "laptops": {"parent": "pilot", "pin": "0000000000000000000000000000000000000000",
                "settings": {"apps.retries": 3}}
  },
  "devices": {
    "lt-1": {"groups": ["laptops"], "hardware": "hw"},
    "lt-2": {"groups": ["laptops"], "hardware": "hw"},
    "kiosk-1": {"groups": ["pilot"], "hardware": "hw"},
    "old-1": {"groups": ["pilot"], "hardware": "hw", "state": "retired"}
  },
  "policies": {
    "baseline": {
      "name": "Baseline", "description": "Everything a workplace gets",
      "settings": {"desktop": "gnome"}, "enforced": ["desktop"],
      "controls": ["BIO 12.3.1", "ISO 27002 8.9"]
    },
    "space": {
      "name": "Room to update", "settings": {},
      "conditions": [{"metric": "disk.free_percent", "op": ">=", "value": 15,
                      "detail": "An update needs room to build and activate."}]
    }
  },
  "filters": {"only-laptops": {"hardware": "hw"}},
  "assignments": [
    {"policy": "baseline", "target": "org"},
    {"policy": "space", "target": "group:laptops", "filter": "only-laptops", "priority": 10}
  ],
  "rollout": {"rings": [
    {"group": "laptops", "name": "canary", "soakMinutes": 60, "minHealthyPercent": 80},
    {"group": "pilot", "requireApproval": true}
  ]}
}`

// twoStatuses reports one healthy device and one that errored, so both the
// happy row and the incident row have something to render.
type twoStatuses struct{ now time.Time }

func (s twoStatuses) rows() []observed.DeviceStatus {
	return []observed.DeviceStatus{
		{Tag: "lt-1", Revision: "abc123", Phase: observed.Running, LastSeen: s.now,
			Usage: observed.Usage{CPUPct: 20, MemUsedMB: 2048, MemTotalMB: 8192,
				DiskUsedGB: 480, DiskTotalGB: 500}, // 4% free: fails the condition
			Integrations: observed.Integrations{"netbird": {State: "up"}}},
		{Tag: "lt-2", Revision: "def456", Phase: observed.Running,
			LastSeen: s.now.Add(-30 * 24 * time.Hour), Error: "activation failed"},
	}
}

func (s twoStatuses) Upsert(context.Context, string, observed.CheckIn, time.Time) (bool, error) {
	return false, nil
}

func (s twoStatuses) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	for _, r := range s.rows() {
		if r.Tag == tag {
			return r, true, nil
		}
	}
	return observed.DeviceStatus{}, false, nil
}

func (s twoStatuses) List(context.Context, string) ([]observed.DeviceStatus, error) {
	return s.rows(), nil
}
func (s twoStatuses) Ping(context.Context) error { return nil }

func populatedConsole(t *testing.T) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": populatedFleet,
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
	inv := app.NewInventoryService(twoStatuses{now: now}, noFacts{}, fixedClock{now}, app.DefaultTenant)
	srv, err := web.New(web.Services{
		Config:     cfg,
		Inventory:  inv,
		Compliance: app.NewComplianceService(cfg, inv, fixedClock{now}),
	}, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func TestPagesRenderWithAPopulatedFleet(t *testing.T) {
	ts := populatedConsole(t)
	paths := []string{
		"/", "/devices", "/devices/lt-1", "/devices/lt-2", "/groups",
		"/settings", "/settings?scope=group:laptops", "/settings?scope=device:lt-1",
		"/policies", "/compliance", "/elevation", "/changes", "/updates", "/org/updates",
		"/updates/rollout", "/access", "/audit", "/profile", "/station",
		"/enroll", "/integrations", "/integrations?scope=group:laptops",
		"/overlays", "/secrets", "/service-accounts", "/notifications",
		"/org", "/mail",
	}
	for _, p := range paths {
		resp, err := client().Get(ts.URL + p)
		if err != nil {
			t.Fatalf("%s: %v", p, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// A page whose service is not wired here may legitimately 404 or 503.
		// The assertion is on pages that answer 200: those must be COMPLETE.
		// render() turns a template error into a 500, so a 500 is a failure
		// with a cause in the log, and a truncated 200 is the quieter variant
		// of the same bug.
		if resp.StatusCode == 500 {
			t.Errorf("%s: 500 - the template failed with a populated fleet", p)
			continue
		}
		if resp.StatusCode != 200 {
			t.Logf("%s: %d (service not wired in this console)", p, resp.StatusCode)
			continue
		}
		if !strings.Contains(string(body), "</html>") {
			t.Errorf("%s: 200 with a truncated document (%d bytes)", p, len(body))
		}
	}
}

// The panel has to render, not just compute. A template block that is only
// ever exercised in production ships invisible - this repo has already had a
// styling change do exactly that - so one page is fetched with an integration
// switched on and its row asserted in the HTML.
func TestDevicePageShowsIntegrationState(t *testing.T) {
	ts := populatedConsole(t)

	fetch := func(path string) string {
		t.Helper()
		resp, err := client().Get(ts.URL + path)
		if err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		if resp.StatusCode != 200 {
			t.Fatalf("%s: %d", path, resp.StatusCode)
		}
		return string(b)
	}

	// lt-1 reports NetBird up.
	body := fetch("/devices/lt-1")
	if !strings.Contains(body, "NetBird VPN") {
		t.Fatal("the integrations panel did not render on a device with one turned on")
	}

	// lt-2 has the same integration turned on and reported nothing about it.
	// That must read as no reading, never as down: an old agent must not make
	// a working mesh look broken.
	body = fetch("/devices/lt-2")
	if !strings.Contains(body, "NetBird VPN") {
		t.Fatal("an enabled integration got no row on a device that never reported it")
	}
	if !strings.Contains(body, "no reading") {
		t.Fatal("an unreported integration is not shown as unmeasured")
	}
}
