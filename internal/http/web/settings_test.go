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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

const seedFleet = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma"}},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hw"}}
}`

const seedCatalog = `[
  {"name":"apps.office","type":"boolean","description":"Office suite","default":false,"riskClass":"high"},
  {"name":"desktop","type":"string","description":"Desktop environment","default":"kde"},
  {"name":"netbird.setupKey","type":"string","description":"NetBird join key","secret":true}
]`

// newConsole builds the console over a real temp git repo seeded with
// fleet.json + catalog.json, dev sessions, allow-all gate.
func newConsole(t *testing.T) (*httptest.Server, *app.ConfigService) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{"fleet.json": seedFleet, "catalog.json": seedCatalog} {
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
	gate := ports.GateFunc(func(context.Context, string, []string) error { return nil })
	cfg, err := app.NewConfigService(repo, gate)
	if err != nil {
		t.Fatal(err)
	}
	srv, err := web.New(web.Services{Config: cfg}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg
}

// noRedirect keeps 3xx visible so tests can assert on them.
func client() *http.Client {
	return &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
}

func TestSettingsPageRendersCatalog(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	for _, want := range []string{"apps.office", "Office suite", "high risk", "desktop",
		`default: <code>&#34;kde&#34;</code>`} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Unknown scope 404s instead of rendering an empty editor.
	resp2, _ := client().Get(ts.URL + "/settings?scope=group:ghost")
	resp2.Body.Close()
	if resp2.StatusCode != 404 {
		t.Fatalf("ghost scope status = %d", resp2.StatusCode)
	}
}

func TestSettingsPostSetEnforceClear(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+"/settings", form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}
	// The batch handler diffs every catalog key's v:<key> against the
	// scope's current settings in one Save: an omitted/empty row clears a
	// key that is currently set. The seed fleet already sets org "desktop",
	// so every org post below must echo v:desktop to keep it - exactly as
	// the settings page always resubmits every row, touched or not.

	// Set + enforce apps.office at org.
	resp := post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"},
		"v:apps.office": {"true"}, "e:apps.office": {"on"}})
	if resp.StatusCode != 303 {
		t.Fatalf("set status = %d", resp.StatusCode)
	}
	own, enforced, _ := cfg.Fleet().ScopeSettings("org")
	if own["apps.office"] != true || len(enforced) != 1 || enforced[0] != "apps.office" {
		t.Fatalf("after set: own=%v enforced=%v", own, enforced)
	}

	// Re-post without the enforce checkbox unlocks.
	post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"}, "v:apps.office": {"false"}})
	own, enforced, _ = cfg.Fleet().ScopeSettings("org")
	if own["apps.office"] != false || len(enforced) != 0 {
		t.Fatalf("after unlock: own=%v enforced=%v", own, enforced)
	}

	// Omitting v:apps.office (empty submitted value on a currently-set key)
	// clears it.
	post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"}})
	own, _, _ = cfg.Fleet().ScopeSettings("org")
	if _, has := own["apps.office"]; has {
		t.Fatalf("after clear: own=%v", own)
	}

	// Group and device scopes take writes too.
	post(url.Values{"scope": {"group:pilot"}, "v:desktop": {"gnome"}})
	own, _, _ = cfg.Fleet().ScopeSettings("group:pilot")
	if own["desktop"] != "gnome" {
		t.Fatalf("group set: own=%v", own)
	}
	post(url.Values{"scope": {"device:lt-1"}, "v:desktop": {"cosmic"}})
	if res := cfg.Fleet().Resolve("lt-1"); res["desktop"].Value != "cosmic" {
		t.Fatalf("device set not resolved: %+v", res["desktop"])
	}

	// Guard rail: a value the catalog type rejects fails the whole batch.
	if resp := post(url.Values{"scope": {"org"}, "v:desktop": {"plasma"}, "v:apps.office": {"maybe"}}); resp.StatusCode != 400 {
		t.Errorf("bad value: status = %d, want 400", resp.StatusCode)
	}
	respCSRF, _ := client().PostForm(ts.URL+"/settings", url.Values{
		"scope": {"org"}, "v:desktop": {"plasma"}, "csrf": {"wrong"}})
	respCSRF.Body.Close()
	if respCSRF.StatusCode != 403 {
		t.Fatalf("bad csrf status = %d", respCSRF.StatusCode)
	}
}

func TestRequireChangeRequestBlocksDirectSetting(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	// Turn on require-change-request (dev session is org owner).
	resp, _ := c.PostForm(ts.URL+"/assurance", url.Values{"csrf": {"dev-csrf"}, "requireChangeRequest": {"on"}})
	resp.Body.Close()
	if !cfg.Fleet().Assurance.RequireChangeRequest {
		t.Fatal("require-change-request not saved")
	}

	// A direct setting edit is now refused (must go through a change).
	resp, _ = c.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "key": {"desktop"}, "action": {"set"}, "value": {"gnome"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("direct setting accepted while change-request is required")
	}
	if !strings.Contains(string(body), "change-request required") {
		t.Fatalf("expected change-request message, got %d\n%s", resp.StatusCode, body)
	}
}
