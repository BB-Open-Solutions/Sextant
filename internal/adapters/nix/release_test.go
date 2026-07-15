package nix

import (
	"context"
	"errors"
	"strings"
	"testing"

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
