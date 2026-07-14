package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestMiddlewareRecordsPanickingRequest proves a handler panic still shows up
// in sextant_http_requests_total/duration (status 500): before the fix, the
// two counter/histogram calls ran AFTER next.ServeHTTP with no defer, so a
// panic unwound straight past them - meaning the exact requests that became
// 500s were invisible on the dashboards meant to surface them.
func TestMiddlewareRecordsPanickingRequest(t *testing.T) {
	m := New()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /boom", func(http.ResponseWriter, *http.Request) {
		panic("kaboom")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/boom", nil)

	func() {
		defer func() { _ = recover() }() // stand in for mw.Recover, outside this middleware
		m.Middleware(mux).ServeHTTP(rec, req)
	}()

	out := scrapeText(t, m)
	if !strings.Contains(out, `route="GET /boom"`) || !strings.Contains(out, `status="500"`) {
		t.Fatalf("panicking request not recorded (status defaulted to 500):\n%s", grepLines(out, "requests_total"))
	}
}

// TestMetricsStatusWriterUnwrap proves http.ResponseController can traverse
// the wrapper to the real ResponseWriter.
func TestMetricsStatusWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	if sw.Unwrap() != rec {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}
}

func scrapeText(t *testing.T, m *Metrics) string {
	t.Helper()
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body, err := io.ReadAll(rec.Body)
	if err != nil {
		t.Fatal(err)
	}
	return string(body)
}
