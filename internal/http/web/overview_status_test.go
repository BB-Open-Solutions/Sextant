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

// The overview page was rendering fine in every test and failing in production
// with "index of untyped nil". The handler built its config-state map and never
// put it in the render data, so the template indexed a nil map - but ONLY
// inside `range .Status`, and the existing smoke console has no inventory
// service, so that loop body never executed. A page can be fully covered and
// completely untested at the same time when the coverage never reaches the
// branch that carries data.
//
// This console has a device WITH a reported status, which is the state the
// overview is actually for.

type oneStatus struct{ st observed.DeviceStatus }

func (s *oneStatus) Upsert(context.Context, string, observed.CheckIn, time.Time) (bool, error) {
	return false, nil
}

func (s *oneStatus) Get(_ context.Context, _, tag string) (observed.DeviceStatus, bool, error) {
	if tag != s.st.Tag {
		return observed.DeviceStatus{}, false, nil
	}
	return s.st, true, nil
}

func (s *oneStatus) List(context.Context, string) ([]observed.DeviceStatus, error) {
	return []observed.DeviceStatus{s.st}, nil
}
func (s *oneStatus) Ping(context.Context) error { return nil }

type noFacts struct{}

func (noFacts) PutFacts(context.Context, string, string, []byte, time.Time) error { return nil }
func (noFacts) GetFacts(context.Context, string, string) ([]byte, time.Time, bool, error) {
	return nil, time.Time{}, false, nil
}

type fixedClock struct{ t time.Time }

func (c fixedClock) Now() time.Time { return c.t }

func consoleWithADeviceThatReported(t *testing.T) *httptest.Server {
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
	// lt-1 is the seed fleet's device. It has checked in just now, so it is
	// online and carries a revision - the case the overview row is written for.
	status := &oneStatus{st: observed.DeviceStatus{
		Tag: "lt-1", Revision: "abc123", Phase: observed.Running, LastSeen: now,
	}}
	inv := app.NewInventoryService(status, noFacts{}, fixedClock{now}, app.DefaultTenant)
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

func TestOverviewRendersWithAReportingDevice(t *testing.T) {
	ts := consoleWithADeviceThatReported(t)
	resp, err := client().Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Fatalf("overview answered %d, want 200:\n%s", resp.StatusCode, body)
	}
	// render() turns a template error into a 500, but assert on the document
	// as well: the failure mode this test exists for produced a page that
	// stopped halfway through the device table.
	if !strings.Contains(string(body), "</html>") {
		t.Fatalf("overview answered 200 with a truncated document (%d bytes)", len(body))
	}
	if !strings.Contains(string(body), "lt-1") {
		t.Error("the reporting device is missing from the overview; the table body did not render")
	}
}
