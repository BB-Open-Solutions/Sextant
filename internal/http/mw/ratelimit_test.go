package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestRateLimitPerClient(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}), RateLimit(rate.Limit(1), 3))

	do := func(addr string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/login", nil)
		req.RemoteAddr = addr
		h.ServeHTTP(rec, req)
		return rec.Code
	}

	// Burst of 3 allowed, the 4th refused.
	for i := 0; i < 3; i++ {
		if got := do("10.0.0.1:1234"); got != 200 {
			t.Fatalf("request %d = %d, want 200", i, got)
		}
	}
	if got := do("10.0.0.1:1234"); got != 429 {
		t.Fatalf("burst overflow = %d, want 429", got)
	}
	// Another client is unaffected.
	if got := do("10.0.0.2:1234"); got != 200 {
		t.Fatalf("other client = %d, want 200", got)
	}
}

// TestRateLimitSharedAcrossMuxRoutes confirms RateLimit is suitable for
// wrapping a whole set of routes (e.g. the entire /api/v1 mux, or a
// station's report/jobs/claim/status group) behind one bucket per client,
// not a separate bucket per path - a client cannot dodge the limit by
// spreading requests across different endpoints on the same wrapped mux.
func TestRateLimitSharedAcrossMuxRoutes(t *testing.T) {
	inner := http.NewServeMux()
	inner.HandleFunc("GET /a", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	inner.HandleFunc("GET /b", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(200) })
	limited := RateLimit(rate.Limit(1), 2)(inner)

	do := func(path, addr string) int {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", path, nil)
		req.RemoteAddr = addr
		limited.ServeHTTP(rec, req)
		return rec.Code
	}

	if got := do("/a", "10.0.0.5:1"); got != 200 {
		t.Fatalf("first /a = %d, want 200", got)
	}
	if got := do("/b", "10.0.0.5:1"); got != 200 {
		t.Fatalf("first /b = %d, want 200", got)
	}
	// The burst of 2 is already spent across /a and /b; a 3rd request to
	// EITHER route from the same client is refused.
	if got := do("/a", "10.0.0.5:1"); got != 429 {
		t.Fatalf("third request (any route) = %d, want 429", got)
	}
}
