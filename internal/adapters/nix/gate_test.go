package nix

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func TestValidateInvocation(t *testing.T) {
	var gotName string
	var gotArgs []string
	g := &EvalGate{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("ok"), nil
	}}
	if err := g.Validate(context.Background(), "/repo", []string{"lt-1"}); err != nil {
		t.Fatal(err)
	}
	if gotName != "nix" {
		t.Fatalf("ran %q", gotName)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, "/repo#nixosConfigurations") {
		t.Errorf("flake ref missing: %s", joined)
	}
	if !strings.Contains(joined, `"lt-1"`) {
		t.Errorf("host not scoped: %s", joined)
	}
	if !strings.Contains(joined, "toplevel.drvPath") {
		t.Errorf("gate must force drvPath, not attrNames: %s", joined)
	}
}

func TestHostVariantsExpand(t *testing.T) {
	g := &EvalGate{HostVariants: []string{"", "-sb"}}
	names, err := g.expandVariants([]string{"lt-1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 2 || names[0] != "lt-1" || names[1] != "lt-1-sb" {
		t.Fatalf("variants not expanded: %v", names)
	}
}

// TestApplyExprRejectsInjectionHost proves the hostRe firewall guards the two
// surfaces that interpolate a host name into a nix expression on the write path
// (every Apply): expandVariants, which builds the name, and applyExprExact,
// which splices it. Neither may trust that an upstream caller validated the
// slug. This is the write-path counterpart to the CI-only Builder gate covered
// by TestBuildInvalidHostRejectedBeforeRunning.
func TestApplyExprRejectsInjectionHost(t *testing.T) {
	g := &EvalGate{}
	for _, bad := range []string{
		`lt-1"; builtins.trace "pwned" null`,
		`lt-1${builtins.readFile "/etc/passwd"}`,
		"../etc/passwd",
		`lt-1"`,
		"UPPER-not-a-slug",
		"",
	} {
		if _, err := g.expandVariants([]string{bad}); err == nil {
			t.Errorf("expandVariants accepted injection-y host %q", bad)
		}
		if _, err := applyExprExact([]string{bad}); err == nil {
			t.Errorf("applyExprExact accepted injection-y host %q", bad)
		}
	}
}

// An empty host list means the whole fleet: the gate discovers the host set
// from nix (cheap attrNames, no config force) and then forces each discovered
// host's toplevel derivation.
func TestEmptyHostsDiscoversAndForcesWholeSet(t *testing.T) {
	var calls [][]string
	g := &EvalGate{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		calls = append(calls, args)
		joined := strings.Join(args, " ")
		if strings.Contains(joined, "attrNames") {
			return []byte(`["host-a","host-b"]`), nil
		}
		return []byte("ok"), nil
	}}
	if err := g.Validate(context.Background(), "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("want attrNames + one eval batch, got %d calls", len(calls))
	}
	if !strings.Contains(strings.Join(calls[0], " "), "attrNames") {
		t.Errorf("first call must discover names via attrNames: %v", calls[0])
	}
	batch := strings.Join(calls[1], " ")
	if !strings.Contains(batch, `"host-a"`) || !strings.Contains(batch, `"host-b"`) ||
		!strings.Contains(batch, "toplevel.drvPath") {
		t.Errorf("eval batch must force the discovered hosts' drvPath: %s", batch)
	}
}

// A large host set is evaluated in memory-bounded batches, each in its own nix
// process, so peak memory does not grow with fleet size.
func TestValidateChunksLargeHostSet(t *testing.T) {
	var batches int
	g := &EvalGate{ChunkSize: 3, run: func(context.Context, string, ...string) ([]byte, error) {
		batches++
		return []byte("ok"), nil
	}}
	hosts := []string{"h1", "h2", "h3", "h4", "h5", "h6", "h7"}
	if err := g.Validate(context.Background(), "/repo", hosts); err != nil {
		t.Fatal(err)
	}
	if batches != 3 { // ceil(7/3)
		t.Fatalf("want 3 batches for 7 hosts at size 3, got %d", batches)
	}
}

// Parallel workers evaluate every batch exactly once and genuinely overlap:
// with 3 workers and 6 batches at least two nix processes must be in flight
// at the same time, or the "workers" knob is decorative.
func TestValidateParallelWorkersCoverAllBatches(t *testing.T) {
	var mu sync.Mutex
	inFlight, peak, calls := 0, 0, 0
	seen := map[string]bool{}
	g := &EvalGate{ChunkSize: 1, Workers: 3,
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			mu.Lock()
			inFlight++
			if inFlight > peak {
				peak = inFlight
			}
			calls++
			seen[strings.Join(args, " ")] = true
			mu.Unlock()
			time.Sleep(20 * time.Millisecond) // hold the slot so overlap is observable
			mu.Lock()
			inFlight--
			mu.Unlock()
			return []byte("ok"), nil
		}}
	hosts := []string{"h1", "h2", "h3", "h4", "h5", "h6"}
	if err := g.Validate(context.Background(), "/repo", hosts); err != nil {
		t.Fatal(err)
	}
	if calls != 6 {
		t.Fatalf("want 6 batch evals, got %d", calls)
	}
	for _, h := range hosts {
		found := false
		for k := range seen {
			if strings.Contains(k, `"`+h+`"`) {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("host %s never evaluated", h)
		}
	}
	if peak < 2 {
		t.Fatalf("no genuine concurrency observed (peak %d)", peak)
	}
}

// A rejection with parallel workers still fails the whole validation and is
// reported as the gate's verdict, and the rejection CANCELS the other workers.
//
// The cancellation half used to be asserted as "fewer than 8 batches ran",
// which is a race dressed as an invariant: a worker already inside run() always
// finishes, so under load (or under -race) all eight could complete and the
// test failed while the code was correct. It had been noted as flaky twice.
// What the gate actually promises is that cancellation REACHES the remaining
// workers - so every batch after the rejection here returns only once its
// context is done, which makes the property deterministic and stronger than a
// count.
func TestValidateParallelPropagatesRejection(t *testing.T) {
	const workers = 4
	var mu sync.Mutex
	calls, cancelled := 0, 0
	// arrived lets the rejecting batch wait until the other workers are really
	// inside run(). Without it the rejection can land before they start, and the
	// test would pass without ever exercising concurrency - the opposite
	// flakiness to the one it used to have.
	arrived := make(chan struct{}, workers)
	g := &EvalGate{ChunkSize: 1, Workers: workers,
		run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			mu.Lock()
			calls++
			first := calls == 1
			mu.Unlock()
			if first {
				for range workers - 1 {
					select {
					case <-arrived:
					case <-time.After(10 * time.Second):
						return nil, errors.New("the other workers never started")
					}
				}
				return []byte("error: bad host"), errors.New("exit 1")
			}
			arrived <- struct{}{}
			// Every other batch waits for the cancellation the rejection must
			// trigger. The timeout is a test failure, not a fallback: if it
			// fires, the gate kept workers running after a verdict.
			select {
			case <-ctx.Done():
				mu.Lock()
				cancelled++
				mu.Unlock()
				return nil, ctx.Err()
			case <-time.After(10 * time.Second):
				return nil, errors.New("cancellation never arrived")
			}
		}}
	err := g.Validate(context.Background(), "/repo",
		[]string{"h1", "h2", "h3", "h4", "h5", "h6", "h7", "h8"})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	mu.Lock()
	defer mu.Unlock()
	if calls < 2 {
		t.Fatalf("only %d batch(es) ran; the test never exercised a second worker", calls)
	}
	if cancelled == 0 {
		t.Fatal("no worker observed the cancellation a rejection must trigger")
	}
	if cancelled != calls-1 {
		t.Fatalf("%d of %d concurrent batches saw the cancellation; every batch after the rejection must", cancelled, calls-1)
	}
}

// A rejection in any batch fails the whole change and stops evaluating the
// rest: later batches must not run once one host is proven invalid.
func TestValidateStopsAtFirstBadBatch(t *testing.T) {
	var batches int
	g := &EvalGate{ChunkSize: 2, run: func(context.Context, string, ...string) ([]byte, error) {
		batches++
		if batches == 1 {
			return []byte("error: bad option 'x'"), errors.New("exit 1")
		}
		return []byte("ok"), nil
	}}
	err := g.Validate(context.Background(), "/repo", []string{"h1", "h2", "h3", "h4"})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	if batches != 1 {
		t.Fatalf("evaluation did not stop at the first bad batch: ran %d", batches)
	}
}

// A scope that resolves to no building hosts (empty fleet) is vacuously valid
// and must not shell nix at all.
func TestValidateEmptyFleetIsVacuouslyValid(t *testing.T) {
	var called bool
	g := &EvalGate{run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
		called = true
		if strings.Contains(strings.Join(args, " "), "attrNames") {
			return []byte(`[]`), nil
		}
		return []byte("ok"), nil
	}}
	if err := g.Validate(context.Background(), "/repo", nil); err != nil {
		t.Fatalf("empty fleet should pass: %v", err)
	}
	if !called {
		t.Fatal("expected the attrNames discovery call")
	}
}

func TestRejectionBecomesValidationError(t *testing.T) {
	g := &EvalGate{run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("error: assertion failed: unknown setting key 'apps.bogus'\nat /nix/store/abc:1\n"), errors.New("exit 1")
	}}
	err := g.Validate(context.Background(), "/repo", nil)
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	if !strings.Contains(verr.Detail, "apps.bogus") {
		t.Errorf("detail lost the reason: %q", verr.Detail)
	}
	if strings.Contains(verr.Detail, "at /nix/store") {
		t.Errorf("store noise not stripped: %q", verr.Detail)
	}
}

func TestSanitizeStripsStorePathsAndTruncates(t *testing.T) {
	long := strings.Repeat("error line\n", 40) +
		"final: /nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-drv.drv rejected\n"
	out := sanitize(long)
	if strings.Contains(out, "/nix/store/aaaa") {
		t.Error("store path not replaced")
	}
	if got := len(strings.Split(out, "\n")); got > 12 {
		t.Errorf("output not truncated: %d lines", got)
	}
	if !strings.Contains(out, "rejected") {
		t.Error("tail (most relevant part) lost")
	}
}
