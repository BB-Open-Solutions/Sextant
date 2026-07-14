package mw

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"golang.org/x/time/rate"
)

func TestClientIP(t *testing.T) {
	mk := func(remote, xff string) *http.Request {
		r := httptest.NewRequest("POST", "/", nil)
		r.RemoteAddr = remote
		if xff != "" {
			r.Header.Set("X-Forwarded-For", xff)
		}
		return r
	}
	// No proxy trust: XFF is ignored (client-controlled), key on RemoteAddr host.
	if got := clientIP(mk("10.0.0.9:5555", "1.2.3.4"), false); got != "10.0.0.9" {
		t.Errorf("untrusted = %q, want 10.0.0.9", got)
	}
	// Trusted proxy: key on the rightmost XFF entry (what the proxy observed),
	// so a spoofed left-hand entry cannot dodge the limit.
	if got := clientIP(mk("10.0.0.1:80", "9.9.9.9, 8.8.8.8, 1.2.3.4"), true); got != "1.2.3.4" {
		t.Errorf("trusted = %q, want 1.2.3.4 (rightmost)", got)
	}
	// Trusted but no XFF: fall back to RemoteAddr.
	if got := clientIP(mk("10.0.0.1:80", ""), true); got != "10.0.0.1" {
		t.Errorf("trusted no-xff = %q, want 10.0.0.1", got)
	}
}

func TestRateLimitPerClient(t *testing.T) {
	h := Chain(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(200)
	}), RateLimit(rate.Limit(1), 3, false))

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
	limited := RateLimit(rate.Limit(1), 2, false)(inner)

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
