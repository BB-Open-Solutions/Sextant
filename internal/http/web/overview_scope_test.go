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
	"regexp"
	"strconv"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// deviceStatCount extracts the rendered "Devices" stat card value: the
// integer in the first `text-headline-md text-ink` div, which immediately
// follows the card carrying the "router" icon (unique to that card).
func deviceStatCount(t *testing.T, page string) int {
	t.Helper()
	idx := strings.Index(page, ">router</span>")
	if idx < 0 {
		t.Fatalf("devices stat card (router icon) not found in page")
	}
	m := regexp.MustCompile(`text-headline-md text-ink">(\d+)</div>`).FindStringSubmatch(page[idx:])
	if m == nil {
		t.Fatalf("no devices stat value found after the router icon")
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("devices stat value %q not an int: %v", m[1], err)
	}
	return n
}

// overviewSeed: a parent group ("region") with one child ("site") so a
// group-scoped overview can be checked for subtree inclusion, plus a device
// outside that hierarchy ("standalone-1") that must never show up once a
// scope is selected. site-team can view "site" only, for the
// read-confidentiality check.
const overviewSeed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"region": {}, "site": {"parent": "region"}, "other": {}},
  "devices": {
    "site-1": {"groups": ["site"], "hardware": "hw"},
    "standalone-1": {"groups": ["other"], "hardware": "hw"}
  },
  "access": [{"group": "site-team", "role": "viewer", "scope": "group:site"}]
}`

// newOverviewConsole builds a console seeded with overviewSeed for one fixed
// user, with an allow-all gate (fleet mutation itself is not under test).
func newOverviewConsole(t *testing.T, u identity.User) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(overviewSeed), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "catalog.json"), []byte(seedCatalog), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")

	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, scopedSessions{u}, true,
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

// TestOverviewScopeDefaultsToOrg checks requirement 4: no ?scope= behaves
// exactly like ?scope=org, seeing every visible device.
func TestOverviewScopeDefaultsToOrg(t *testing.T) {
	ts := newOverviewConsole(t, identity.User{Subject: "root", Service: true})
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}
	codeDefault, pageDefault := get("/")
	codeOrg, pageOrg := get("/?scope=org")
	if codeDefault != 200 || codeOrg != 200 {
		t.Fatalf("status default=%d org=%d, want 200", codeDefault, codeOrg)
	}
	if pageDefault != pageOrg {
		t.Fatal("default scope must render identically to explicit ?scope=org")
	}
	for _, tag := range []string{"site-1", "standalone-1"} {
		if !strings.Contains(pageDefault, tag) {
			t.Errorf("org scope missing device %q", tag)
		}
	}
}

// TestOverviewScopeGroupSubtreeAndDeviceFilter checks requirement 2: a group
// scope includes its subtree and excludes unrelated devices, a device scope
// shows exactly one, and stats/selector state line up with the filtered set.
func TestOverviewScopeGroupSubtreeAndDeviceFilter(t *testing.T) {
	ts := newOverviewConsole(t, identity.User{Subject: "root", Service: true})
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	// Baseline: org scope counts both devices.
	_, orgPage := get("/?scope=org")
	if n := deviceStatCount(t, orgPage); n != 2 {
		t.Fatalf("org devices stat = %d, want 2", n)
	}

	// "region" is the parent of "site", and "site-1" belongs to "site"
	// directly (not "region"): the subtree rule must still pull it into the
	// parent group's scope, so the device-count stat reads 1, not 0.
	// (The scope selector's own device dropdown mirrors settingsPage's
	// direct-membership-only cascade, so it alone would not prove this -
	// the stat card is computed straight from the subtree filter.)
	code, page := get("/?scope=group:region")
	if code != 200 {
		t.Fatalf("group scope = %d, want 200", code)
	}
	if n := deviceStatCount(t, page); n != 1 {
		t.Errorf("group:region devices stat = %d, want 1 (site-1 via subtree, standalone-1 excluded)", n)
	}
	// Selector state: the group pill should read as selected for this scope.
	if !strings.Contains(page, `value="group:region"`) {
		t.Error("scope selector missing group:region option")
	}

	// A device scope shows exactly that device and nothing else.
	code, page = get("/?scope=device:standalone-1")
	if code != 200 {
		t.Fatalf("device scope = %d, want 200", code)
	}
	if n := deviceStatCount(t, page); n != 1 {
		t.Errorf("device:standalone-1 devices stat = %d, want 1", n)
	}

	// An unknown scope answers like a missing page, not a 500 or a silent
	// fallback to org (which would leak the whole fleet under a typo'd URL).
	if code, _ := get("/?scope=group:does-not-exist"); code != 404 {
		t.Errorf("unknown group scope = %d, want 404", code)
	}
	if code, _ := get("/?scope=device:does-not-exist"); code != 404 {
		t.Errorf("unknown device scope = %d, want 404", code)
	}
}

// TestOverviewScopeReadConfidentiality checks requirement 1: a scope the
// viewer cannot read answers like a 404, mirroring settingsPage. site-team
// only holds a viewer binding at group:site, so its own scope must render
// but the parent group (which also covers the unrelated "other" subtree)
// must not.
func TestOverviewScopeReadConfidentiality(t *testing.T) {
	ts := newOverviewConsole(t, identity.User{Subject: "u", Groups: []string{"site-team"}})
	get := func(path string) (int, string) {
		t.Helper()
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b)
	}

	if code, page := get("/?scope=group:site"); code != 200 || !strings.Contains(page, "site-1") {
		t.Fatalf("own scope = %d, want 200 with site-1 visible", code)
	}
	if code, _ := get("/?scope=group:region"); code != 404 {
		t.Errorf("ancestor scope (out of the granted binding) = %d, want 404", code)
	}
	if code, _ := get("/?scope=device:standalone-1"); code != 404 {
		t.Errorf("unrelated device scope = %d, want 404", code)
	}
}
