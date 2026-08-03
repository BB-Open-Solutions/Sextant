package nix

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// Publish must realise the targets first and then copy them - signed - into
// the cache: nix copy does not build, so the order is the correctness.
func TestPublishBuildsThenCopiesSigned(t *testing.T) {
	var calls [][]string
	p := &Publisher{CacheDir: "/data/cache", KeyFile: "/secrets/cache-key",
		HostVariants: []string{"", "-sb"},
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			calls = append(calls, args)
			return []byte("ok"), nil
		}}
	if err := p.Publish(context.Background(), "/repo", []string{"lt-1"}); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 {
		t.Fatalf("want build then copy, got %d calls", len(calls))
	}
	build := strings.Join(calls[0], " ")
	if !strings.HasPrefix(build, "build ") || !strings.Contains(build, `"lt-1"`) ||
		!strings.Contains(build, `"lt-1-sb"`) || !strings.Contains(build, "toplevel") {
		t.Errorf("build call wrong: %s", build)
	}
	cp := strings.Join(calls[1], " ")
	if !strings.HasPrefix(cp, "copy ") ||
		!strings.Contains(cp, "file:///data/cache?secret-key=/secrets/cache-key") {
		t.Errorf("copy call wrong (must sign into the cache): %s", cp)
	}
}

// A failing build surfaces as a ValidationError and never reaches the copy:
// nothing unbuilt may be published.
func TestPublishStopsOnBuildFailure(t *testing.T) {
	var calls int
	p := &Publisher{CacheDir: "/c", KeyFile: "/k",
		run: func(context.Context, string, ...string) ([]byte, error) {
			calls++
			return []byte("error: build broke"), errors.New("exit 1")
		}}
	err := p.Publish(context.Background(), "/repo", []string{"lt-1"})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	if calls != 1 {
		t.Fatalf("copy ran after a failed build (%d calls)", calls)
	}
}

// An unconfigured publisher refuses rather than silently publishing unsigned.
func TestPublishRequiresConfiguration(t *testing.T) {
	p := &Publisher{CacheDir: "", KeyFile: ""}
	if err := p.Publish(context.Background(), "/repo", []string{"lt-1"}); err == nil {
		t.Fatal("unconfigured publisher accepted work")
	}
}

// The injection firewall guards the publish path too.
func TestPublishRejectsInjectionHost(t *testing.T) {
	p := &Publisher{CacheDir: "/c", KeyFile: "/k",
		run: func(context.Context, string, ...string) ([]byte, error) {
			t.Fatal("run must not be reached for an invalid host")
			return nil, nil
		}}
	if err := p.Publish(context.Background(), "/repo",
		[]string{`x"; builtins.exec "pwn`}); err == nil {
		t.Fatal("injection-y host accepted")
	}
}

// Batching the release is the point of the change, so pin that one nix
// process never receives the whole ring.
func TestPublishBatchesInsteadOfLoadingTheWholeRing(t *testing.T) {
	var builds, copies [][]string
	p := &Publisher{
		CacheDir: "/cache", KeyFile: "/key", ChunkSize: 2,
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			switch args[0] {
			case "build":
				builds = append(builds, args)
			case "copy":
				copies = append(copies, args)
			}
			return nil, nil
		},
	}
	if err := p.Publish(context.Background(), "/repo", []string{"h1", "h2", "h3", "h4", "h5"}); err != nil {
		t.Fatal(err)
	}
	if len(builds) != 3 || len(copies) != 3 {
		t.Fatalf("5 hosts at chunk 2 ran %d builds and %d copies, want 3 and 3", len(builds), len(copies))
	}
	for _, b := range builds {
		if n := len(b) - 3; n > 2 { // argv: build --no-link --no-warn-dirty <targets...>
			t.Errorf("one nix process was handed %d targets, above the chunk of 2", n)
		}
	}
}

// A ring that fits must not be split: batching is a memory bound, not a
// policy, and every extra invocation re-pays the fixed cost.
func TestPublishDoesNotSplitWhatFits(t *testing.T) {
	builds := 0
	p := &Publisher{
		CacheDir: "/cache", KeyFile: "/key", ChunkSize: 10,
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			if args[0] == "build" {
				builds++
			}
			return nil, nil
		},
	}
	if err := p.Publish(context.Background(), "/repo", []string{"h1", "h2"}); err != nil {
		t.Fatal(err)
	}
	if builds != 1 {
		t.Errorf("2 hosts at chunk 10 ran %d builds, want 1", builds)
	}
}

// A killed build is not a rejected one. Reporting it as "build runner
// unreachable" sent somebody looking at the network for a memory problem.
func TestAKilledBuildSaysKilledAndNotRejected(t *testing.T) {
	for name, tc := range map[string]struct {
		out        string
		wantKilled bool
	}{
		"oom kill leaves no diagnostic":  {"building '/nix/store/x.drv'...\nsignal: killed\n", true},
		"kernel wording":                 {"building...\nKilled\n", true},
		"a real rejection is not a kill": {"error: The option `dawo.apps' does not exist.", false},
		// The tell is the ABSENCE of a diagnostic. A build that failed on
		// merit and happens to mention memory stays a rejection.
		"a diagnostic wins over the word memory": {"error: builder failed: out of memory while linking", false},
	} {
		err := buildFailure(context.Background(), "building the release", time.Minute, []byte(tc.out))
		if got := strings.Contains(err.Error(), "was killed"); got != tc.wantKilled {
			t.Errorf("%s: killed=%v, want %v (%v)", name, got, tc.wantKilled, err)
		}
	}
}
