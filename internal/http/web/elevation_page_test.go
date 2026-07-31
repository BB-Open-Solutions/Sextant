package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

type memElevStore struct{ m map[string]elevation.Request }

func (s *memElevStore) Put(_ context.Context, _ string, r elevation.Request) error {
	s.m[r.ID] = r
	return nil
}

func (s *memElevStore) Get(_ context.Context, _, id string) (elevation.Request, bool, error) {
	r, ok := s.m[id]
	return r, ok, nil
}

func (s *memElevStore) Pending(_ context.Context, _ string) ([]elevation.Request, error) {
	out := make([]elevation.Request, 0, len(s.m))
	for _, r := range s.m {
		out = append(out, r)
	}
	return out, nil
}

func elevationConsole(t *testing.T) (*httptest.Server, *app.ElevationService, *memElevStore) {
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
	store := &memElevStore{m: map[string]elevation.Request{}}
	svc := app.NewElevationService(store, fixedClock{time.Now()}, app.DefaultTenant)
	srv, err := web.New(web.Services{Config: cfg, Elevation: svc}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, svc, store
}

func TestElevationPageShowsWhoIsWaiting(t *testing.T) {
	ts, svc, _ := elevationConsole(t)
	if _, err := svc.Raise(context.Background(), "lt-1", "bbuijs",
		"org.freedesktop.NetworkManager.settings.modify.system", "joining the office wifi"); err != nil {
		t.Fatal(err)
	}
	resp, err := client().Get(ts.URL + "/elevation")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("page = %d, want 200", resp.StatusCode)
	}
	page := string(body)
	for _, want := range []string{"bbuijs", "lt-1", "joining the office wifi"} {
		if !strings.Contains(page, want) {
			t.Errorf("the page does not show %q; an approver cannot judge what they cannot read", want)
		}
	}
	// The reported action is shown, and labelled as reported: PAM never sees
	// the polkit action id, so this string comes from the device's own
	// session. A page that presented it as established fact would invite an
	// approver to trust the one thing here that is not verified.
	if !strings.Contains(page, "modify.system") {
		t.Error("the reported action is missing from the page")
	}
}

// An empty queue is the normal state. It has to read as "nobody is waiting"
// rather than as a broken page, or an operator will go looking for a fault
// every time the fleet is quiet.
func TestElevationPageIsCalmWhenEmpty(t *testing.T) {
	ts, _, _ := elevationConsole(t)
	resp, err := client().Get(ts.URL + "/elevation")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "</html>") {
		t.Fatalf("empty queue = %d, truncated=%v", resp.StatusCode, !strings.Contains(string(body), "</html>"))
	}
}

func TestElevationApproveAndDenyBothRecordADecision(t *testing.T) {
	ts, svc, store := elevationConsole(t)
	ctx := context.Background()

	yes, err := svc.Raise(ctx, "lt-1", "bbuijs", "", "")
	if err != nil {
		t.Fatal(err)
	}
	no, err := svc.Raise(ctx, "lt-1", "someone", "", "")
	if err != nil {
		t.Fatal(err)
	}

	post := func(id, decision string) int {
		resp, err := client().PostForm(ts.URL+"/elevation/"+id,
			url.Values{"decision": {decision}, "csrf": {"dev-csrf"}})
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		return resp.StatusCode
	}

	if code := post(yes.ID, "approve"); code != http.StatusSeeOther {
		t.Fatalf("approve = %d, want 303", code)
	}
	if code := post(no.ID, "deny"); code != http.StatusSeeOther {
		t.Fatalf("deny = %d, want 303", code)
	}

	if got := store.m[yes.ID]; got.State != elevation.Approved || got.DecidedBy == "" {
		t.Errorf("approved request stored as %+v; state and approver must both be recorded", got)
	}
	// A denial has to be stored as a decision too, not left looking unanswered
	// - otherwise the device keeps polling a request somebody already refused.
	if got := store.m[no.ID]; got.State != elevation.Denied || got.DecidedBy == "" {
		t.Errorf("denied request stored as %+v; a refusal is a decision", got)
	}
}
