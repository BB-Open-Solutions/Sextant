package health

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"testing"
	"time"
)

func TestLivenessAlwaysOK(t *testing.T) {
	r := New(time.Second)
	rec := httptest.NewRecorder()
	r.Liveness().ServeHTTP(rec, httptest.NewRequest("GET", "/healthz", nil))
	if rec.Code != 200 {
		t.Fatalf("liveness = %d, want 200", rec.Code)
	}
}

func TestReadinessAllPass(t *testing.T) {
	r := New(time.Second)
	r.Register("git", func(context.Context) error { return nil })
	r.Register("db", func(context.Context) error { return nil })

	rec := httptest.NewRecorder()
	r.Readiness().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 200 {
		t.Fatalf("readiness = %d, want 200; body %s", rec.Code, rec.Body)
	}
	var body struct {
		Ready  bool
		Checks map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if !body.Ready || body.Checks["git"] != "ok" || body.Checks["db"] != "ok" {
		t.Errorf("body = %+v", body)
	}
}

func TestReadinessFailingCheck(t *testing.T) {
	r := New(time.Second)
	r.Register("git", func(context.Context) error { return nil })
	r.Register("db", func(context.Context) error { return errors.New("connection refused") })

	rec := httptest.NewRecorder()
	r.Readiness().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
	if rec.Code != 503 {
		t.Fatalf("readiness = %d, want 503", rec.Code)
	}
	var body struct {
		Ready  bool
		Checks map[string]string
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Ready || body.Checks["db"] != "connection refused" {
		t.Errorf("body = %+v", body)
	}
}

func TestReadinessCheckTimeout(t *testing.T) {
	r := New(10 * time.Millisecond)
	r.Register("slow", func(ctx context.Context) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return nil
		}
	})

	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() {
		r.Readiness().ServeHTTP(rec, httptest.NewRequest("GET", "/readyz", nil))
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("readiness did not respect per-check timeout")
	}
	if rec.Code != 503 {
		t.Fatalf("readiness = %d, want 503 on timeout", rec.Code)
	}
}
