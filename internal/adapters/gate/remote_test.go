package gate

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
