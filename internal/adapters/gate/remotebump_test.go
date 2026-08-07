package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// remotebump_test.go covers the core-update bump, which was at 0%.
//
// This is how a core update becomes a change request: the runner computes a
// flake.lock pinning one input to its upstream head, and the console commits
// the returned lock onto the change branch. What must never happen is that a
// refusal reads as a success - the console would then commit an empty or
// stale lock as if it were a real core bump, and the gate would evaluate a
// tree nobody meant to build.

func bumpSrv(t *testing.T, status int, body string) (*httptest.Server, *[]byte) {
	t.Helper()
	var got []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func TestBumpReturnsTheLockAndRevision(t *testing.T) {
	srv, body := bumpSrv(t, 200,
		`{"ok":true,"lock":"{\"nodes\":{}}","rev":"deadbeefcafe"}`)
	lock, rev, err := NewRemoteGate(srv.URL).WithToken("s3cr3t").BumpInput(context.Background(), "dawo")
	if err != nil {
		t.Fatalf("bump: %v", err)
	}
	if rev != "deadbeefcafe" {
		t.Errorf("rev = %q", rev)
	}
	if !strings.Contains(string(lock), "nodes") {
		t.Errorf("lock = %q", lock)
	}
	// The runner has to be told WHICH input, or it bumps something else and
	// the console commits a lock for a change nobody asked for.
	var sent map[string]string
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if sent["input"] != "dawo" {
		t.Errorf("sent input %q, want dawo", sent["input"])
	}
}

// TestARefusedBumpIsNeverASuccess is the property this file exists for. Each
// case is a way the runner can say no, and every one of them must reach the
// caller as an error rather than as an empty lock.
func TestARefusedBumpIsNeverASuccess(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"explicit refusal", 200, `{"ok":false,"error":"input dawo not found in flake.nix"}`},
		{"refusal with no reason", 200, `{"ok":false}`},
		{"server error", 500, `{"ok":false,"error":"nix ran out of memory"}`},
		{"unauthorized", 401, `no`},
		{"body that is not JSON", 200, `<html>proxy error</html>`},
		{"empty body", 200, ``},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := bumpSrv(t, c.status, c.body)
			lock, rev, err := NewRemoteGate(srv.URL).BumpInput(context.Background(), "dawo")
			if err == nil {
				t.Fatalf("no error; the console would commit lock=%q rev=%q as a real bump", lock, rev)
			}
			if len(lock) != 0 || rev != "" {
				t.Errorf("a refusal still returned lock=%q rev=%q", lock, rev)
			}
		})
	}
}

func TestBumpSurfacesTheRunnersReason(t *testing.T) {
	// The reason is what an operator reads on a failed core update. Without
	// it they see "bump refused" and have to go and find the runner's log.
	srv, _ := bumpSrv(t, 200, `{"ok":false,"error":"input dawo not found in flake.nix"}`)
	_, _, err := NewRemoteGate(srv.URL).BumpInput(context.Background(), "dawo")
	if err == nil {
		t.Fatal("no error")
	}
	if !strings.Contains(err.Error(), "not found in flake.nix") {
		t.Errorf("error does not carry the runner's reason: %v", err)
	}
}

func TestBumpAgainstAnUnreachableRunnerIsAnError(t *testing.T) {
	srv, _ := bumpSrv(t, 200, `{"ok":true}`)
	url := srv.URL
	srv.Close()
	if _, _, err := NewRemoteGate(url).BumpInput(context.Background(), "dawo"); err == nil {
		t.Fatal("an unreachable runner produced no error")
	}
}
