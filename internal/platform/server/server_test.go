package server

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func discard() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestServeAndCleanShutdown(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "hello")
	})
	srv := New("127.0.0.1:0", h, discard(), Options{ShutdownGrace: 2 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()

	addr := srv.Addr()
	resp, err := http.Get("http://" + addr + "/")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "hello" {
		t.Errorf("body = %q", body)
	}

	cancel()
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("Run returned %v, want nil on clean shutdown", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

// TestShutdownDrainsInflight proves the graceful path: a request in flight
// when shutdown starts still completes.
func TestShutdownDrainsInflight(t *testing.T) {
	started := make(chan struct{})
	var finished atomic.Bool
	h := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		time.Sleep(300 * time.Millisecond) // simulates a git commit in flight
		finished.Store(true)
		fmt.Fprint(w, "done")
	})
	srv := New("127.0.0.1:0", h, discard(), Options{ShutdownGrace: 5 * time.Second})

	ctx, cancel := context.WithCancel(context.Background())
	runErr := make(chan error, 1)
	go func() { runErr <- srv.Run(ctx) }()
	addr := srv.Addr()

	respCh := make(chan string, 1)
	go func() {
		resp, err := http.Get("http://" + addr + "/")
		if err != nil {
			respCh <- "error: " + err.Error()
			return
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		respCh <- string(b)
	}()

	<-started // request is in flight
	cancel()  // SIGTERM equivalent

	if got := <-respCh; got != "done" {
		t.Errorf("in-flight response = %q, want %q", got, "done")
	}
	if !finished.Load() {
		t.Error("handler did not finish before shutdown returned")
	}
	if err := <-runErr; err != nil {
		t.Fatalf("Run returned %v", err)
	}
}

func TestListenFailure(t *testing.T) {
	srv := New("256.256.256.256:99999", http.NewServeMux(), discard(), Options{})
	err := srv.Run(context.Background())
	if err == nil {
		t.Fatal("want listen error, got nil")
	}
}
