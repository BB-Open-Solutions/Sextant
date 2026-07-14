package web_test

import (
	"context"
	"encoding/base64"
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
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memDevSecrets is an in-memory ports.DeviceSecretStore for handler tests.
type memDevSecrets struct {
	ciph map[string][]byte
	meta map[string]secret.Meta
}

func newMemDevSecrets() *memDevSecrets {
	return &memDevSecrets{ciph: map[string][]byte{}, meta: map[string]secret.Meta{}}
}

func dk(tag string, k secret.Kind) string { return tag + "|" + string(k) }

func (s *memDevSecrets) Put(_ context.Context, _, tag string, kind secret.Kind, c []byte, by string, now time.Time) error {
	s.ciph[dk(tag, kind)] = c
	s.meta[dk(tag, kind)] = secret.Meta{Tag: tag, Kind: kind, CreatedBy: by, Created: now.UTC().Format(time.RFC3339)}
	return nil
}
func (s *memDevSecrets) Get(_ context.Context, _, tag string, kind secret.Kind) ([]byte, secret.Meta, bool, error) {
	c, ok := s.ciph[dk(tag, kind)]
	if !ok {
		return nil, secret.Meta{}, false, nil
	}
	return c, s.meta[dk(tag, kind)], true, nil
}
func (s *memDevSecrets) List(_ context.Context, _, tag string) ([]secret.Meta, error) {
	var out []secret.Meta
	for _, m := range s.meta {
		if m.Tag == tag {
			out = append(out, m)
		}
	}
	return out, nil
}
func (s *memDevSecrets) MarkRevealed(_ context.Context, _, tag string, kind secret.Kind, by string, now time.Time) error {
	m := s.meta[dk(tag, kind)]
	m.RevealedBy, m.Revealed = by, now.UTC().Format(time.RFC3339)
	s.meta[dk(tag, kind)] = m
	return nil
}

func webSealer(t *testing.T) ports.Sealer {
	t.Helper()
	s, err := secretbox.New(base64.StdEncoding.EncodeToString(make([]byte, 32)))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

// newSecretConsole builds a console with a device-secret store holding a sealed
// LUKS key for one device, so reveal and the wizard reveal-control can be tested.
func newSecretConsole(t *testing.T, dev bool) (*httptest.Server, *memDevSecrets) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		if out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	_ = os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(seedStationFleet), 0o644)
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

	store := newMemDevSecrets()
	secrets := app.NewDeviceSecretsService(store, webSealer(t), clockNow{}, "")
	if err := secrets.Store(context.Background(), "lt-1", secret.LUKS, "z7Xq-9pLm-R2wK", "svc:station"); err != nil {
		t.Fatal(err)
	}

	srv, err := web.New(web.Services{Config: cfg, DeviceSecrets: secrets},
		web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	_ = dev
	return ts, store
}

func TestSecretRevealShowsOnceAndAudits(t *testing.T) {
	ts, store := newSecretConsole(t, true)
	c := client()

	resp, _ := c.PostForm(ts.URL+"/devices/lt-1/secret/luks/reveal",
		url.Values{"csrf": {"dev-csrf"}})
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("reveal = %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "z7Xq-9pLm-R2wK") {
		t.Fatal("revealed page does not show the secret")
	}
	// The reveal is audited in the store (who + when).
	if m := store.meta[dk("lt-1", secret.LUKS)]; m.RevealedBy == "" || m.Revealed == "" {
		t.Fatalf("reveal not recorded: %+v", m)
	}
}

func TestSecretRevealUnknownIs404(t *testing.T) {
	ts, _ := newSecretConsole(t, true)
	resp, _ := client().PostForm(ts.URL+"/devices/lt-1/secret/admin/reveal",
		url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("reveal of missing secret = %d, want 404", resp.StatusCode)
	}
}
