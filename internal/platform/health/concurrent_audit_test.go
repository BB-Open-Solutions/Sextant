package health

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"
	"time"
)

// TestSnapshotRunsChecksConcurrently proves N slow checks cost roughly one
// check's latency, not N times it: before this fix, Snapshot ran checks
// sequentially, so an orchestrator's /readyz probe could take N*timeout and
// exceed the probe's own deadline, flapping readiness.
func TestSnapshotRunsChecksConcurrently(t *testing.T) {
	r := New(time.Second)
	const n = 5
	const delay = 100 * time.Millisecond
	for i := 0; i < n; i++ {
		r.Register(string(rune('a'+i)), func(ctx context.Context) error {
			time.Sleep(delay)
			return nil
		})
	}
	start := time.Now()
	ready, results := r.Snapshot(context.Background())
	elapsed := time.Since(start)
	if !ready || len(results) != n {
		t.Fatalf("ready=%v results=%+v", ready, results)
	}
	// Sequential would take ~n*delay (500ms); concurrent should stay well
	// under that even with scheduling slack.
	if elapsed > delay*time.Duration(n)/2 {
		t.Fatalf("snapshot took %s, want well under %s (checks not concurrent?)", elapsed, delay*time.Duration(n))
	}
}

// TestSnapshotHidesErrorDetailButLogsIt: the unauthenticated JSON/HTML
// surface must carry a generic reason, never the raw dependency error (which
// can embed a DSN, remote URL, or issuer detail) - but the real error must
// still reach the server-side log for operators.
func TestSnapshotHidesErrorDetailButLogsIt(t *testing.T) {
	var buf bytes.Buffer
	r := New(time.Second)
	r.SetLogger(slog.New(slog.NewTextHandler(&buf, nil)))
	r.Register("db", func(context.Context) error {
		return errDetailed
	})

	ready, results := r.Snapshot(context.Background())
	if ready {
		t.Fatal("ready = true with a failing check")
	}
	if len(results) != 1 || results[0].Info == errDetailed.Error() {
		t.Fatalf("results = %+v: raw error text leaked into the public result", results)
	}
	if results[0].Info != "unavailable" {
		t.Fatalf("results[0].Info = %q, want a generic reason", results[0].Info)
	}
	if !strings.Contains(buf.String(), errDetailed.Error()) {
		t.Fatalf("log output = %q, want it to contain the real error detail", buf.String())
	}
}

type detailedErr string

func (e detailedErr) Error() string { return string(e) }

const errDetailed = detailedErr("dial postgres://ops:s3cret@10.0.0.5:5432/sextant: connection refused")
