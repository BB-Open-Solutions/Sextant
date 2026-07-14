package mw

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestAccessLogRecordsPanickingRequest proves a handler panic still produces
// one access-log line (status defaulted to 500) instead of vanishing: before
// the fix, the log call sat AFTER next.ServeHTTP with no defer, so a panic
// unwound straight past it (mw.Recover lives outside AccessLog in the real
// chain and is not part of this test - Recover itself is what turns the
// panic into a response; this test only proves AccessLog's own bookkeeping
// survives the unwind).
func TestAccessLogRecordsPanickingRequest(t *testing.T) {
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	panicking := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		panic("boom")
	})

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/panics", nil)

	func() {
		defer func() { _ = recover() }() // stand in for mw.Recover, outside AccessLog
		AccessLog(log)(panicking).ServeHTTP(rec, req)
	}()

	line := buf.String()
	if !strings.Contains(line, "path=/panics") {
		t.Fatalf("access log missing the panicking request: %q", line)
	}
	if !strings.Contains(line, "status=500") {
		t.Fatalf("access log did not default status to 500 on panic: %q", line)
	}
}

// TestStatusWriterUnwrap proves http.ResponseController can traverse the
// wrapper to the real ResponseWriter (needed for Flush/Hijack/
// SetWriteDeadline on a streaming or large-download handler further down the
// chain).
func TestStatusWriterUnwrap(t *testing.T) {
	rec := httptest.NewRecorder()
	sw := &statusWriter{ResponseWriter: rec, status: http.StatusOK}
	if sw.Unwrap() != rec {
		t.Fatal("Unwrap did not return the underlying ResponseWriter")
	}
}
