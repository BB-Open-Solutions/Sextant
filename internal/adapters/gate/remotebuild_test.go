package gate

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// remotebuild_test.go covers the pre-build that has to succeed before a wave
// promotes (build-before-promote). It was at 0% while carrying a distinction
// the rollout depends on:
//
//	a build the runner REPORTS as failed  -> BuildFailed, and the run halts
//	a runner we cannot reach or that 500s -> an error, and the run HOLDS
//
// Collapsing those two makes an unreachable runner look like a broken fleet
// config and stops a rollout that had nothing wrong with it. Nothing in the
// type system keeps them apart, so it is pinned here.

// buildSrv returns a server answering /build with the given status and body,
// plus a pointer to the last request it saw.
func buildSrv(t *testing.T, status int, body string) (*httptest.Server, *http.Request, *[]byte) {
	t.Helper()
	var last http.Request
	var lastBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		last = *r
		lastBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(srv.Close)
	return srv, &last, &lastBody
}

func TestEnsureBuiltPhases(t *testing.T) {
	cases := []struct {
		name string
		body string
		want ports.BuildPhase
	}{
		{"done", `{"phase":"done"}`, ports.BuildDone},
		{"failed", `{"phase":"failed","detail":"host x: attribute missing"}`, ports.BuildFailed},
		{"building", `{"phase":"building","detail":"3/17"}`, ports.BuildBuilding},
		// An unknown phase must not read as done. The runner is a separate
		// binary on its own release cadence, so a phase this build has never
		// heard of is the normal consequence of a version skew - and
		// "keep waiting" is the only safe reading of it.
		{"unknown phase is treated as still building", `{"phase":"marinating"}`, ports.BuildBuilding},
		{"empty phase is treated as still building", `{}`, ports.BuildBuilding},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _, _ := buildSrv(t, 200, c.body)
			b := NewRemoteBuilder(srv.URL, "")
			got, err := b.EnsureBuilt(context.Background(), "abc123", []string{"lt-1"})
			if err != nil {
				t.Fatalf("EnsureBuilt: %v", err)
			}
			if got.Phase != c.want {
				t.Errorf("phase = %v, want %v", got.Phase, c.want)
			}
		})
	}
}

func TestEnsureBuiltCarriesTheFailureDetail(t *testing.T) {
	srv, _, _ := buildSrv(t, 200, `{"phase":"failed","detail":"host lt-1: infinite recursion"}`)
	got, err := NewRemoteBuilder(srv.URL, "").EnsureBuilt(context.Background(), "r", nil)
	if err != nil {
		t.Fatal(err)
	}
	// The detail is what an operator reads on a halted wave. A BuildFailed
	// with an empty detail is a dead end: the console can only say "the build
	// failed" and the reason lives in a log nobody has.
	if !strings.Contains(got.Detail, "infinite recursion") {
		t.Errorf("detail = %q, want the runner's reason", got.Detail)
	}
}

// TestEnsureBuiltSendsWhatTheRunnerNeeds: the revision and the host list are
// the whole request. Sending the wrong revision builds the wrong thing and
// still reports done.
func TestEnsureBuiltSendsWhatTheRunnerNeeds(t *testing.T) {
	srv, req, body := buildSrv(t, 200, `{"phase":"done"}`)
	b := NewRemoteBuilder(srv.URL, "s3cr3t")
	if _, err := b.EnsureBuilt(context.Background(), "deadbeef", []string{"lt-1", "lt-2"}); err != nil {
		t.Fatal(err)
	}
	if req.URL.Path != "/build" || req.Method != http.MethodPost {
		t.Errorf("called %s %s, want POST /build", req.Method, req.URL.Path)
	}
	if got := req.Header.Get("Authorization"); got != "Bearer s3cr3t" {
		t.Errorf("Authorization = %q", got)
	}
	var sent buildRequest
	if err := json.Unmarshal(*body, &sent); err != nil {
		t.Fatalf("request body: %v", err)
	}
	if sent.Rev != "deadbeef" || len(sent.Hosts) != 2 {
		t.Errorf("sent %+v, want rev deadbeef and 2 hosts", sent)
	}
}

// TestEnsureBuiltRunnerTroubleIsAnErrorNotAFailedBuild is the distinction
// this file exists for.
func TestEnsureBuiltRunnerTroubleIsAnErrorNotAFailedBuild(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "no"},
		{"runner error", 500, "boom"},
		{"not found", 404, "no such endpoint"},
		{"garbage body with 200", 200, "this is not json"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _, _ := buildSrv(t, c.status, c.body)
			got, err := NewRemoteBuilder(srv.URL, "").EnsureBuilt(context.Background(), "r", nil)
			if err == nil {
				t.Fatalf("no error for %s; caller would read this as a verdict", c.name)
			}
			if got.Phase == ports.BuildFailed {
				t.Errorf("runner trouble reported as BuildFailed: a rollout would halt on a runner problem")
			}
		})
	}
}

func TestEnsureBuiltUnreachableRunnerIsAnError(t *testing.T) {
	srv, _, _ := buildSrv(t, 200, `{"phase":"done"}`)
	url := srv.URL
	srv.Close() // nothing is listening now
	got, err := NewRemoteBuilder(url, "").EnsureBuilt(context.Background(), "r", nil)
	if err == nil {
		t.Fatal("an unreachable runner produced no error")
	}
	if got.Phase == ports.BuildFailed {
		t.Error("an unreachable runner reported as BuildFailed; the run would halt instead of holding")
	}
}

// TestEnsureBuiltHonoursTheTimeout: the poll call is bounded so a runner that
// accepts the connection and then says nothing cannot wedge the rollout tick.
func TestEnsureBuiltHonoursTheTimeout(t *testing.T) {
	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never answers within the test's patience
	}))
	t.Cleanup(func() { close(block); srv.Close() })

	b := NewRemoteBuilder(srv.URL, "")
	b.Timeout = 50 * time.Millisecond
	start := time.Now()
	if _, err := b.EnsureBuilt(context.Background(), "r", nil); err == nil {
		t.Fatal("a hanging runner produced no error")
	}
	if el := time.Since(start); el > 5*time.Second {
		t.Errorf("took %v; the timeout did not bound the call", el)
	}
}

func TestCancelBuildsIsBestEffortButReportsTrouble(t *testing.T) {
	srv, req, _ := buildSrv(t, 200, "")
	if err := NewRemoteBuilder(srv.URL, "tok").CancelBuilds(context.Background()); err != nil {
		t.Fatalf("cancel: %v", err)
	}
	if req.URL.Path != "/build/cancel" {
		t.Errorf("called %s, want /build/cancel", req.URL.Path)
	}

	bad, _, _ := buildSrv(t, 503, "busy")
	if err := NewRemoteBuilder(bad.URL, "").CancelBuilds(context.Background()); err == nil {
		t.Error("a refusing runner produced no error; the caller cannot log what it does not learn")
	}
}
