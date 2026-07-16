package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestClientSendsBearerToken is the low-level counterpart to the CLI-level
// happy-path tests below: it exercises client.do directly against a fake
// server and asserts the exact Authorization header sxctl sends, so the
// "token via env goes out as Bearer" contract has one test that can't be
// confused by table/JSON formatting on either side.
func TestClientSendsBearerToken(t *testing.T) {
	var gotAuth, gotContentType string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotContentType = r.Header.Get("Content-Type")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	c := newClient(srv.URL, "s3cr3t-token", &out)
	var res map[string]any
	if err := c.do("POST", "/api/v1/whatever", map[string]any{"x": 1}, &res); err != nil {
		t.Fatalf("do: %v", err)
	}
	if want := "Bearer s3cr3t-token"; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
	if gotContentType != "application/json" {
		t.Errorf("Content-Type header = %q, want application/json", gotContentType)
	}
}

// TestClientAPIError401 covers the client's error-shaping contract for a
// failed request: a 401 with a JSON {"error": "..."} body must come back
// as an *apiError carrying the status and the server's message verbatim,
// not a generic decode failure.
func TestClientAPIError401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	var out bytes.Buffer
	c := newClient(srv.URL, "bad-token", &out)
	var res map[string]any
	err := c.do("GET", "/api/v1/devices", nil, &res)
	if err == nil {
		t.Fatal("do: want error for 401, got nil")
	}
	var ae *apiError
	if !errors.As(err, &ae) {
		t.Fatalf("err = %T (%v), want *apiError", err, err)
	}
	if ae.Status != http.StatusUnauthorized {
		t.Errorf("Status = %d, want 401", ae.Status)
	}
	if ae.Msg != "invalid token" {
		t.Errorf("Msg = %q, want %q", ae.Msg, "invalid token")
	}
}

// runAgainst points sxctl (via SEXTANT_URL/SEXTANT_TOKEN, the same
// production config path main() uses) at a test server and runs it,
// returning the exit code and captured stdout/stderr.
func runAgainst(t *testing.T, srv *httptest.Server, args ...string) (code int, stdout, stderr string) {
	t.Helper()
	t.Setenv("SEXTANT_URL", srv.URL)
	t.Setenv("SEXTANT_TOKEN", "test-token")
	var outBuf, errBuf bytes.Buffer
	code = run(args, &outBuf, &errBuf)
	return code, outBuf.String(), errBuf.String()
}

// TestRunDevicesListHappyPath drives sxctl end-to-end through run() against
// a fake /api/v1/devices, covering the default (table) output path and,
// again, the Bearer header - this time as the CLI actually assembles it via
// SEXTANT_TOKEN, not via a direct client.do call.
func TestRunDevicesListHappyPath(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/devices" {
			t.Errorf("request = %s %s, want GET /api/v1/devices", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode([]map[string]any{
			{"tag": "nuc-01", "class": "station", "hardware": "nuc11", "groups": []string{"ops"}},
		})
	}))
	defer srv.Close()

	code, out, errOut := runAgainst(t, srv, "devices", "list")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut)
	}
	if !strings.Contains(out, "TAG") || !strings.Contains(out, "nuc-01") {
		t.Errorf("stdout = %q, want a table with TAG header and nuc-01 row", out)
	}
	if want := "Bearer test-token"; gotAuth != want {
		t.Errorf("Authorization header = %q, want %q", gotAuth, want)
	}
}

// TestRunRolloutStatusHappyPath covers the printJSON output path (as
// opposed to the table path above) via a second subcommand.
func TestRunRolloutStatusHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/rollout" {
			t.Errorf("request = %s %s, want GET /api/v1/rollout", r.Method, r.URL.Path)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"phase": "idle", "target": "none"})
	}))
	defer srv.Close()

	code, out, errOut := runAgainst(t, srv, "rollout", "status")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut)
	}
	if !strings.Contains(out, `"phase": "idle"`) {
		t.Errorf("stdout = %q, want pretty-printed JSON containing phase: idle", out)
	}
}

// TestRunTokensMintHappyPath covers a POST subcommand: it asserts the
// request body sxctl sends (id/name/ceiling) and that the minted secret -
// the one piece of output that matters here - reaches stdout.
func TestRunTokensMintHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/tokens" {
			t.Errorf("request = %s %s, want POST /api/v1/tokens", r.Method, r.URL.Path)
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["id"] != "my-token" || body["name"] != "My Token" || body["ceiling"] != "viewer" {
			t.Errorf("body = %#v, want id=my-token name=%q ceiling=viewer", body, "My Token")
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"secret": "sxctl_abc123"})
	}))
	defer srv.Close()

	code, out, errOut := runAgainst(t, srv, "tokens", "mint", "My Token", "-ceiling", "viewer")

	if code != 0 {
		t.Fatalf("exit code = %d, want 0; stderr = %q", code, errOut)
	}
	if out != "sxctl_abc123\n" {
		t.Errorf("stdout = %q, want %q", out, "sxctl_abc123\n")
	}
}

// TestRunDevicesList401 covers the CLI-level failure path for an
// authentication error: it must surface as a clean, single-line message
// (not a stack trace or raw JSON) and exit code 1, distinguishing a
// request/auth failure from the usage failures (exit 2) covered elsewhere.
func TestRunDevicesList401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"invalid token"}`))
	}))
	defer srv.Close()

	code, out, errOut := runAgainst(t, srv, "devices", "list")

	if code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if !strings.Contains(errOut, "sxctl: 401: invalid token") {
		t.Errorf("stderr = %q, want it to contain the 401 message", errOut)
	}
	if out != "" {
		t.Errorf("stdout = %q, want empty on error", out)
	}
}
