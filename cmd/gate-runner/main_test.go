package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func discardLog() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func TestAuthorized(t *testing.T) {
	cases := []struct {
		name  string
		token string // server's configured token
		auth  string // caller's Authorization header
		want  bool
	}{
		{"no token configured accepts anything", "", "", true},
		{"no token configured accepts a bearer too", "", "Bearer whatever", true},
		{"correct bearer", "s3cr3t", "Bearer s3cr3t", true},
		{"missing header", "s3cr3t", "", false},
		{"wrong token", "s3cr3t", "Bearer nope", false},
		{"wrong scheme", "s3cr3t", "Basic s3cr3t", false},
		{"empty bearer value", "s3cr3t", "Bearer ", false},
		{"prefix collision, not equal", "s3cr3t", "Bearer s3cr3", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := &server{token: c.token}
			req := httptest.NewRequest(http.MethodPost, "/validate", nil)
			if c.auth != "" {
				req.Header.Set("Authorization", c.auth)
			}
			if got := s.authorized(req); got != c.want {
				t.Errorf("authorized() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestHandleValidateRejectsUnauthorizedBeforeParsingBody(t *testing.T) {
	// The server has no workdir/gate configured; if handleValidate ever got
	// past the auth check it would panic or fail on the nil gate/git calls.
	// A garbage, non-JSON body must still be rejected with 401 (not 400),
	// proving authentication runs before the body is read/decoded.
	s := &server{token: "s3cr3t", log: discardLog()}

	req := httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader("not json at all"))
	rec := httptest.NewRecorder()
	s.handleValidate(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusUnauthorized, rec.Body.String())
	}
	if got := rec.Header().Get("WWW-Authenticate"); got == "" {
		t.Errorf("missing WWW-Authenticate header")
	}
}

func TestHandleValidateAcceptsCorrectBearer(t *testing.T) {
	// With the right token, the request clears auth and proceeds to body
	// parsing - a bad JSON body should now fail as 400, not 401, showing the
	// auth check let it through to the next stage.
	s := &server{token: "s3cr3t", log: discardLog()}

	req := httptest.NewRequest(http.MethodPost, "/validate", strings.NewReader("not json at all"))
	req.Header.Set("Authorization", "Bearer s3cr3t")
	rec := httptest.NewRecorder()
	s.handleValidate(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d; body=%s", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8090": true,
		"localhost:8090": true,
		"[::1]:8090":     true,
		"0.0.0.0:8090":   false,
		":8090":          false,
		"gate.svc:8090":  false,
	}
	for addr, want := range cases {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}
