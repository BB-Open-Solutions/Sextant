package oidc

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// The Graph fallback runs on the LOGIN path and only for users in more than
// ~150 groups - administrators, in practice. Any error there became a 502 that
// failed the whole sign-in, so a single dropped packet or one rate-limit
// response logged out the people with the most rights. These tests pin the
// retry that fixes it, and the boundary of what may be retried.

func graphServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, *http.Client) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv, srv.Client()
}

func TestGraphRetriesTransientFailure(t *testing.T) {
	var calls atomic.Int32
	srv, client := graphServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"value":[{"id":"g1","displayName":"Admins"}]}`))
	})

	got, err := fetchGroupsFromGraph(context.Background(), client, srv.URL, "tok")
	if err != nil {
		t.Fatalf("a 503 on the first attempt failed the whole login: %v", err)
	}
	if calls.Load() != 2 {
		t.Fatalf("want 2 attempts, got %d", calls.Load())
	}
	if len(got) == 0 || !strings.Contains(strings.Join(got, ","), "Admins") {
		t.Fatalf("groups not returned after the retry: %v", got)
	}
}

func TestGraphRetriesRateLimitAndHonoursRetryAfter(t *testing.T) {
	var calls atomic.Int32
	srv, client := graphServer(t, func(w http.ResponseWriter, _ *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"value":[]}`))
	})

	start := time.Now()
	if _, err := fetchGroupsFromGraph(context.Background(), client, srv.URL, "tok"); err != nil {
		t.Fatalf("a 429 failed the login: %v", err)
	}
	// Retry-After said one second; the default back-off would be 200ms, so
	// waiting at least most of a second proves the header was read rather than
	// ignored - ignoring it is how a retry storm against Graph starts.
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond {
		t.Fatalf("Retry-After was ignored: retried after %v", elapsed)
	}
}

// A rejected token is not transient. Retrying it delays the real answer and
// hammers the identity provider with a request it has already refused.
func TestGraphDoesNotRetryClientError(t *testing.T) {
	var calls atomic.Int32
	srv, client := graphServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
	})

	_, err := fetchGroupsFromGraph(context.Background(), client, srv.URL, "tok")
	if err == nil {
		t.Fatal("a 401 was not reported as an error")
	}
	if calls.Load() != 1 {
		t.Fatalf("a 401 was retried %d times; only 429 and 5xx are transient", calls.Load())
	}
}

func TestGraphGivesUpAndSaysSo(t *testing.T) {
	var calls atomic.Int32
	srv, client := graphServer(t, func(w http.ResponseWriter, _ *http.Request) {
		calls.Add(1)
		w.WriteHeader(http.StatusBadGateway)
	})

	_, err := fetchGroupsFromGraph(context.Background(), client, srv.URL, "tok")
	if err == nil {
		t.Fatal("a permanently failing Graph returned no error")
	}
	if int(calls.Load()) != graphAttempts {
		t.Fatalf("want %d attempts, got %d", graphAttempts, calls.Load())
	}
	if !strings.Contains(err.Error(), "attempts") {
		t.Fatalf("the error does not say it gave up after retrying: %v", err)
	}
}

// A cancelled request must stop waiting immediately rather than sleeping out
// its back-off: the operator has already closed the tab.
func TestGraphRetryRespectsContext(t *testing.T) {
	srv, client := graphServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Retry-After", "5")
		w.WriteHeader(http.StatusTooManyRequests)
	})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_, _ = fetchGroupsFromGraph(ctx, client, srv.URL, "tok")
		close(done)
	}()
	time.Sleep(100 * time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancelling the request did not interrupt the retry back-off")
	}
}
