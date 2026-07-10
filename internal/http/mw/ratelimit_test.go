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
