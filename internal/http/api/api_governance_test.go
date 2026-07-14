package api

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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// apiOverFleet serves the API over a repo seeded with the given fleet document
// plus the shared catalog, an allow-all gate and write enabled.
func apiOverFleet(t *testing.T, fleetDoc string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": fleetDoc, "catalog.json": seedCatalog} {
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
	svc, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{}, testToken, true,
		slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// TestAPIEnforcesGovernanceAndValidation is the regression guard for the audit
// finding that the JSON API bypassed the governance, catalog, typing and
// secret-reference checks the console enforces. Every invariant is now owned by
// ConfigService, so the API must reject the same edits the web page would.
func TestAPIEnforcesGovernanceAndValidation(t *testing.T) {
	// Change-request mandated: a direct API edit is a 409, never silently applied.
	gov := `{"version":3,"assurance":{"requireChangeRequest":true},` +
		`"org":{"settings":{}},"groups":{"pilot":{}},` +
		`"devices":{"lt-1":{"groups":["pilot"],"hardware":"hw"}}}`
	srv := apiOverFleet(t, gov)
	if code, body := call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "apps.office", "value": true}, testToken); code != 409 {
		t.Errorf("direct edit under change-request = %d, want 409: %v", code, body)
	}
	// The clear path (DELETE) shares the same governance check as set.
	if code, body := call(t, srv, "DELETE", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "desktop"}, testToken); code != 409 {
		t.Errorf("direct clear under change-request = %d, want 409: %v", code, body)
	}

	// Governance off: the API still enforces catalog membership and secret-ref
	// integrity that used to live only in the web handler.
	open := `{"version":3,"org":{"settings":{}},"groups":{"pilot":{}},` +
		`"devices":{"lt-1":{"groups":["pilot"],"hardware":"hw"}}}`
	srv2 := apiOverFleet(t, open)

	// An unknown key is a 400, not a silent write of a bogus setting.
	if code, _ := call(t, srv2, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "not.in.catalog", "value": true}, testToken); code != 400 {
		t.Errorf("unknown key = %d, want 400", code)
	}
	// A secret setting pinned to an unregistered reference is a 400, so a
	// setting can never dangle at a name that resolves to nothing on-device.
	if code, _ := call(t, srv2, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "netbird.setupKey", "value": "ghost-ref"}, testToken); code != 400 {
		t.Errorf("dangling secret ref = %d, want 400", code)
	}

	// A body that is not valid JSON is a 400 from decode(), on both the set
	// and the clear path - never a 500 from an unhandled parse panic.
	for _, m := range []string{"POST", "DELETE"} {
		req, _ := http.NewRequest(m, srv2.URL+"/api/v1/settings", strings.NewReader("{not json"))
		req.Header.Set("Authorization", "Bearer "+testToken)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != 400 {
			t.Errorf("%s malformed body = %d, want 400", m, resp.StatusCode)
		}
	}
}
