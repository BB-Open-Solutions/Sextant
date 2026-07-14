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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// TestSettingsTextSuggestionsRenderDatalist covers the datalist enhancement:
// a text-typed catalog entry with a known suggestion list (currently just
// autoUpdate.options.repoUrl, see settings.go's textSuggestions) gets a
// list="sugg-..." attribute and a matching <datalist>, while an ordinary
// text entry with no seeded suggestions stays a plain input.
func TestSettingsTextSuggestionsRenderDatalist(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	fleet := `{"version": 3, "org": {"settings": {}}, "groups": {}, "devices": {}}`
	catalog := `[
	  {"name":"autoUpdate.options.repoUrl","type":"string","description":"Update source repo"},
	  {"name":"desktop","type":"string","description":"Desktop environment"}
	]`
	for name, body := range map[string]string{"fleet.json": fleet, "catalog.json": catalog} {
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

	c := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	}}
	resp, err := c.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)

	const wantID = "sugg-autoupdate-options-repourl"
	if !strings.Contains(page, `list="`+wantID+`"`) {
		t.Errorf("repoUrl input missing list=%q attribute\n%s", wantID, page)
	}
	if !strings.Contains(page, `<datalist id="`+wantID+`">`) {
		t.Errorf("page missing datalist#%s", wantID)
	}
	if !strings.Contains(page, `value="https://code.overheid.nl/MinBZK/DAWO-NixOS.git"`) {
		t.Errorf("page missing seeded suggestion option")
	}

	// The desktop entry has no seeded suggestions: its input must stay plain
	// (no list attribute, no matching datalist).
	if strings.Contains(page, `sugg-desktop`) {
		t.Errorf("desktop entry unexpectedly got a suggestion list\n%s", page)
	}
}
