package gate

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckSyntax(t *testing.T) {
	// Server that echoes a canned parse verdict per the code it receives.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/parse" {
			w.WriteHeader(404)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		// A magic body decides the verdict.
		var b [64]byte
		n, _ := r.Body.Read(b[:])
		if containsSub(string(b[:n]), "bad") {
			_, _ = w.Write([]byte(`{"error":"syntax error: at line 3:5"}`))
		} else {
			_, _ = w.Write([]byte(`{"ok":true}`))
		}
	}))
	defer srv.Close()

	g := NewRemoteGate(srv.URL)
	if msg, err := g.CheckSyntax(context.Background(), "{ ok = true; }"); err != nil || msg != "" {
		t.Fatalf("good parse: msg=%q err=%v", msg, err)
	}
	if msg, err := g.CheckSyntax(context.Background(), "{ bad"); err != nil || msg == "" {
		t.Fatalf("bad parse should return a message: msg=%q err=%v", msg, err)
	}

	// Unreachable gate: an error, not a false "valid".
	g2 := NewRemoteGate("http://127.0.0.1:1")
	if _, err := g2.CheckSyntax(context.Background(), "{}"); err == nil {
		t.Fatal("unreachable gate should error, not report valid")
	}
}

func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
