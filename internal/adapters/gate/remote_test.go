package gate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func repoWithFleet(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(`{"version":3}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRemoteGateAccepts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	g := NewRemoteGate(srv.URL)
	if err := g.Validate(context.Background(), repoWithFleet(t), []string{"lt-1"}); err != nil {
		t.Fatalf("accept: %v", err)
	}
}

func TestRemoteGateRejects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		w.Write([]byte(`{"ok":false,"error":"option dawo.bogus does not exist"}`))
	}))
	defer srv.Close()

	g := NewRemoteGate(srv.URL)
	err := g.Validate(context.Background(), repoWithFleet(t), nil)
	var ve *ports.ValidationError
	if !errors.As(err, &ve) {
		t.Fatalf("want ValidationError, got %v", err)
	}
	if ve.Detail == "" {
		t.Fatal("rejection lost the reason")
	}
}

func TestRemoteGateFailsClosedWhenUnreachable(t *testing.T) {
	// A URL that refuses connections: the gate must reject, not wave through.
	g := NewRemoteGate("http://127.0.0.1:1")
	err := g.Validate(context.Background(), repoWithFleet(t), nil)
	if err == nil {
		t.Fatal("unreachable runner must fail closed")
	}
	var ve *ports.ValidationError
	if errors.As(err, &ve) {
		t.Fatal("infrastructure failure must not read as a config rejection")
	}
}

func TestRemoteGateServerErrorIsNotRejection(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte("boom"))
	}))
	defer srv.Close()

	g := NewRemoteGate(srv.URL)
	err := g.Validate(context.Background(), repoWithFleet(t), nil)
	if err == nil {
		t.Fatal("500 must fail closed")
	}
	var ve *ports.ValidationError
	if errors.As(err, &ve) {
		t.Fatal("a runner 500 is not a config rejection")
	}
}

func TestRemoteGateMissingFleet(t *testing.T) {
	g := NewRemoteGate("http://example.invalid")
	if err := g.Validate(context.Background(), t.TempDir(), nil); err == nil {
		t.Fatal("missing fleet.json must error")
	}
}

// TestRemoteGateSurfacesInfraDetail: an infrastructure failure must arrive at
// the operator carrying its cause. Finding this on production cost a pod-log
// expedition for a sentence the runner had already written down; the console
// showed only the JSON document and its fixed classification.
//
// Still an error, not a rejection: a broken runner is not a verdict.
func TestRemoteGateSurfacesInfraDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"ok":false,"error":"staging candidate failed","detail":"nothing to commit, working tree clean"}`))
	}))
	defer srv.Close()

	err := NewRemoteGate(srv.URL).Validate(context.Background(), repoWithFleet(t), []string{"lt-1"})
	if err == nil {
		t.Fatal("a 500 must not read as acceptance")
	}
	var ve *ports.ValidationError
	if errors.As(err, &ve) {
		t.Fatal("an infrastructure failure must not be reported as a gate rejection")
	}
	if !strings.Contains(err.Error(), "nothing to commit") {
		t.Fatalf("the cause did not reach the operator: %v", err)
	}
	if strings.Contains(err.Error(), `{"ok"`) {
		t.Fatalf("the raw JSON document is still being rendered: %v", err)
	}
}

// TestRemoteGateOlderRunnerWithoutDetail: a runner that predates the detail
// field must still produce a readable message rather than an empty one.
func TestRemoteGateOlderRunnerWithoutDetail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		w.Write([]byte(`{"ok":false,"error":"overlay sync failed"}`))
	}))
	defer srv.Close()

	err := NewRemoteGate(srv.URL).Validate(context.Background(), repoWithFleet(t), []string{"lt-1"})
	if err == nil || !strings.Contains(err.Error(), "overlay sync failed") {
		t.Fatalf("classification lost: %v", err)
	}
}
