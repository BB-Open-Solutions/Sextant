package api

import (
	"bytes"
	"context"
	"encoding/json"
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

const seed = `{
  "version": 3,
  "org": {"settings": {"desktop": "plasma", "secureboot": true}, "enforced": ["secureboot"]},
  "groups": {"pilot": {}},
  "devices": {"lt-1": {"groups": ["pilot"], "hardware": "hp-g4", "class": "laptop"}}
}`

const testToken = "test-token-123"

// seedCatalog is the settings vocabulary the API tests write, so ConfigService
// (which now validates every setting against the catalog on both transports)
// accepts the keys these tests exercise.
const seedCatalog = `[
  {"name":"apps.office","type":"boolean","description":"Office suite","default":false,"riskClass":"high"},
  {"name":"apps.bogus","type":"boolean","description":"Test option","default":false},
  {"name":"netbird.setupKey","type":"string","description":"NetBird join key","secret":true},
  {"name":"x","type":"number","description":"Test number","default":0}
]`

// writeSeed writes fleet.json + catalog.json into a repo dir for the API tests.
func writeSeed(t *testing.T, dir string) {
	t.Helper()
	for name, body := range map[string]string{"fleet.json": seed, "catalog.json": seedCatalog} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// newTestAPI serves the API over a seeded temp repo with an allow-all gate.
func newTestAPI(t *testing.T, write bool) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeSeed(t, dir)
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
	New(Services{Config: svc}, Authz{}, testToken, write, slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// call performs an authed request and returns status + parsed body.
func call(t *testing.T, srv *httptest.Server, method, path string, body any, token string) (int, map[string]any) {
	t.Helper()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, _ := http.NewRequest(method, srv.URL+path, rd)
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	if out == nil {
		out = map[string]any{"_raw": string(raw)}
	}
	return resp.StatusCode, out
}

func TestAuth(t *testing.T) {
	srv := newTestAPI(t, true)
	if code, _ := call(t, srv, "GET", "/api/v1/fleet", nil, ""); code != 401 {
		t.Errorf("no token = %d, want 401", code)
	}
	if code, _ := call(t, srv, "GET", "/api/v1/fleet", nil, "wrong"); code != 401 {
		t.Errorf("wrong token = %d, want 401", code)
	}
	if code, _ := call(t, srv, "GET", "/api/v1/fleet", nil, testToken); code != 200 {
		t.Errorf("valid token = %d, want 200", code)
	}
}

func TestReadOnlyModeRefusesWrites(t *testing.T) {
	srv := newTestAPI(t, false)
	code, _ := call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "x", "value": 1}, testToken)
	if code != 403 {
		t.Fatalf("write in read-only = %d, want 403", code)
	}
	if code, _ := call(t, srv, "GET", "/api/v1/devices", nil, testToken); code != 200 {
		t.Errorf("read in read-only = %d, want 200", code)
	}
}

func TestDeviceReadAndResolve(t *testing.T) {
	srv := newTestAPI(t, true)
	code, body := call(t, srv, "GET", "/api/v1/devices/lt-1", nil, testToken)
	if code != 200 {
		t.Fatalf("status %d: %v", code, body)
	}
	resolved, _ := json.Marshal(body["resolved"])
	if !strings.Contains(string(resolved), `"secureboot"`) ||
		!strings.Contains(string(resolved), `"enforced":true`) {
		t.Errorf("resolve missing enforced secureboot: %s", resolved)
	}
	if code, _ := call(t, srv, "GET", "/api/v1/devices/ghost", nil, testToken); code != 404 {
		t.Errorf("unknown device = %d, want 404", code)
	}
}

func TestSettingWriteFlow(t *testing.T) {
	srv := newTestAPI(t, true)
	enforce := true
	code, body := call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "group:pilot", "key": "apps.office", "value": true, "enforce": &enforce}, testToken)
	if code != 200 {
		t.Fatalf("post setting = %d: %v", code, body)
	}
	// Read it back through resolve.
	_, dev := call(t, srv, "GET", "/api/v1/devices/lt-1", nil, testToken)
	resolved, _ := json.Marshal(dev["resolved"])
	if !strings.Contains(string(resolved), `"apps.office"`) {
		t.Fatalf("setting not resolved: %s", resolved)
	}
	// Clear it again.
	code, _ = call(t, srv, "DELETE", "/api/v1/settings",
		map[string]any{"scope": "group:pilot", "key": "apps.office"}, testToken)
	if code != 200 {
		t.Fatalf("delete setting = %d", code)
	}
	// Bad scope is a 400, not a 500.
	code, _ = call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "group:ghost", "key": "x", "value": 1}, testToken)
	if code != 400 {
		t.Errorf("bad scope = %d, want 400", code)
	}
}

func TestPolicyLifecycleOverAPI(t *testing.T) {
	srv := newTestAPI(t, true)

	if code, b := call(t, srv, "PUT", "/api/v1/filters/laptops", map[string]any{
		"rules": []map[string]any{{"attr": "class", "op": "eq", "value": "laptop"}},
	}, testToken); code != 200 {
		t.Fatalf("put filter = %d: %v", code, b)
	}
	if code, b := call(t, srv, "PUT", "/api/v1/policies/vpn", map[string]any{
		"settings": map[string]any{"netbird.enable": true},
		"enforced": []string{"netbird.enable"},
	}, testToken); code != 200 {
		t.Fatalf("put policy = %d: %v", code, b)
	}
	if code, b := call(t, srv, "POST", "/api/v1/assignments", map[string]any{
		"policy": "vpn", "target": "org", "filter": "laptops",
	}, testToken); code != 200 {
		t.Fatalf("assign = %d: %v", code, b)
	}

	// The laptop resolves the enforced policy value with policy provenance.
	_, dev := call(t, srv, "GET", "/api/v1/devices/lt-1", nil, testToken)
	resolved, _ := json.Marshal(dev["resolved"])
	if !strings.Contains(string(resolved), `"netbird.enable"`) ||
		!strings.Contains(string(resolved), `"policy":"vpn"`) {
		t.Fatalf("policy not applied with provenance: %s", resolved)
	}

	// Deleting an assigned policy is a 400; unassign then delete succeeds.
	if code, _ := call(t, srv, "DELETE", "/api/v1/policies/vpn", nil, testToken); code != 400 {
		t.Errorf("delete assigned policy = %d, want 400", code)
	}
	if code, _ := call(t, srv, "DELETE", "/api/v1/assignments", map[string]any{
		"policy": "vpn", "target": "org", "filter": "laptops",
	}, testToken); code != 200 {
		t.Errorf("unassign failed")
	}
	if code, _ := call(t, srv, "DELETE", "/api/v1/policies/vpn", nil, testToken); code != 200 {
		t.Errorf("delete policy failed")
	}
	if code, _ := call(t, srv, "DELETE", "/api/v1/filters/laptops", nil, testToken); code != 200 {
		t.Errorf("delete filter failed")
	}
}

func TestGateRejectionIs422(t *testing.T) {
	// A rejecting gate turns a write into a 422 with the reason.
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeSeed(t, dir)
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, _ := git.Open(dir, "")
	svc, err := app.NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error {
		return &ports.ValidationError{Detail: "assertion failed: unknown option"}
	}))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{}, testToken, true, slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	code, body := call(t, srv, "POST", "/api/v1/settings",
		map[string]any{"scope": "org", "key": "apps.bogus", "value": true}, testToken)
	if code != 422 {
		t.Fatalf("gate rejection = %d, want 422: %v", code, body)
	}
	if !strings.Contains(body["error"].(string), "unknown option") {
		t.Errorf("reason lost: %v", body)
	}
}

func TestDisabledAPIWithoutToken(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	writeSeed(t, dir)
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, _ := git.Open(dir, "")
	svc, _ := app.NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	mux := http.NewServeMux()
	New(Services{Config: svc}, Authz{}, "", true, slog.New(slog.NewTextHandler(io.Discard, nil))).Routes(mux)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	if code, _ := call(t, srv, "GET", "/api/v1/fleet", nil, "anything"); code != 403 {
		t.Fatalf("unconfigured api = %d, want 403", code)
	}
}
