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
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// --- in-memory stores for the self-service surface ---

type memTokens struct {
	mu   sync.Mutex
	toks map[string]token.Token
}

func (m *memTokens) Put(_ context.Context, t token.Token) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.toks[t.ID] = t
	return nil
}
func (m *memTokens) Get(_ context.Context, id string) (token.Token, bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.toks[id]
	return t, ok, nil
}
func (m *memTokens) ListBySubject(_ context.Context, subject string) ([]token.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []token.Token
	for _, t := range m.toks {
		if t.Subject == subject {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memTokens) ListByKind(_ context.Context, kind token.Kind) ([]token.Token, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []token.Token
	for _, t := range m.toks {
		if t.Kind == kind {
			out = append(out, t)
		}
	}
	return out, nil
}
func (m *memTokens) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.toks, id)
	return nil
}
func (m *memTokens) TouchLastUsed(context.Context, string, time.Time) error { return nil }

type memPrefs struct {
	mu sync.Mutex
	m  map[string]identity.Preferences
}

func (p *memPrefs) GetPrefs(_ context.Context, tenant, subject string) (identity.Preferences, bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	v, ok := p.m[tenant+"/"+subject]
	return v, ok, nil
}
func (p *memPrefs) PutPrefs(_ context.Context, tenant, subject string, v identity.Preferences, _ time.Time) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.m[tenant+"/"+subject] = v
	return nil
}

type clockNow struct{}

func (clockNow) Now() time.Time { return time.Now() }

// newProfileConsole: console with token+prefs services over a seeded repo.
func newProfileConsole(t *testing.T) (*httptest.Server, *memTokens) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedFleet), 0o644); err != nil {
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
	tokens := &memTokens{toks: map[string]token.Token{}}
	prefs := &memPrefs{m: map[string]identity.Preferences{}}
	srv, err := web.New(web.Services{
		Config: cfg,
		Tokens: app.NewTokenService(tokens, clockNow{}, 0),
		Prefs:  prefs,
	}, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, tokens
}

func TestProfileSelfService(t *testing.T) {
	ts, store := newProfileConsole(t)
	jar := client()

	// Page renders with prefs form and empty token list.
	resp, _ := jar.Get(ts.URL + "/profile")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "My API tokens") {
		t.Fatalf("profile = %d", resp.StatusCode)
	}

	// Save preferences; invalid timezone rejected.
	form := url.Values{"csrf": {"dev-csrf"}, "timezone": {"Europe/Amsterdam"}, "locale": {"nl"}}
	resp, _ = jar.PostForm(ts.URL+"/profile/prefs", form)
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("prefs save = %d", resp.StatusCode)
	}
	bad := url.Values{"csrf": {"dev-csrf"}, "timezone": {"Mars/Olympus"}}
	resp, _ = jar.PostForm(ts.URL+"/profile/prefs", bad)
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("bad tz = %d, want 400", resp.StatusCode)
	}

	// Mint a personal token: redirect carries the one-shot secret cookie.
	resp, _ = jar.PostForm(ts.URL+"/profile/tokens",
		url.Values{"csrf": {"dev-csrf"}, "name": {"ci"}, "ceiling": {"viewer"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("mint = %d", resp.StatusCode)
	}
	var minted string
	for _, c := range resp.Cookies() {
		if c.Name == "sextant_minted" {
			minted = c.Value
		}
	}
	if minted == "" {
		t.Fatal("no one-shot secret cookie")
	}
	if len(store.toks) != 1 {
		t.Fatalf("store has %d tokens", len(store.toks))
	}
	var id string
	for k, tok := range store.toks {
		id = k
		if tok.Subject != "dev" || tok.Ceiling != "viewer" {
			t.Fatalf("token = %+v", tok)
		}
	}

	// Revoke own token; foreign/unknown id 400s.
	resp, _ = jar.PostForm(ts.URL+"/profile/tokens/"+id+"/revoke",
		url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != 303 || len(store.toks) != 0 {
		t.Fatalf("revoke = %d, left %d", resp.StatusCode, len(store.toks))
	}
	resp, _ = jar.PostForm(ts.URL+"/profile/tokens/ghost/revoke",
		url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("ghost revoke = %d, want 400", resp.StatusCode)
	}
}
