package nix

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// build_test.go mirrors gate_test.go: Builder shares EvalGate's injectable
// runner seam but, unlike the eval gate, had no coverage of its own before
// this file.

func TestBuildInvocation(t *testing.T) {
	var gotName string
	var gotArgs []string
	b := &Builder{run: func(_ context.Context, name string, args ...string) ([]byte, error) {
		gotName, gotArgs = name, args
		return []byte("ok"), nil
	}}
	if err := b.Build(context.Background(), "/repo", []string{"lt-1"}); err != nil {
		t.Fatal(err)
	}
	if gotName != "nix" {
		t.Fatalf("ran %q", gotName)
	}
	// Exact argument vector: "nix build --no-link --no-warn-dirty <target>".
	want := []string{"build", "--no-link", "--no-warn-dirty",
		`/repo#nixosConfigurations."lt-1".config.system.build.toplevel`}
	if len(gotArgs) != len(want) {
		t.Fatalf("args = %q, want %q", gotArgs, want)
	}
	for i := range want {
		if gotArgs[i] != want[i] {
			t.Fatalf("arg[%d] = %q, want %q (full: %q)", i, gotArgs[i], want[i], gotArgs)
		}
	}
}

func TestBuildHostVariantsExpand(t *testing.T) {
	var gotArgs []string
	b := &Builder{
		HostVariants: []string{"", "-sb"},
		run: func(_ context.Context, _ string, args ...string) ([]byte, error) {
			gotArgs = args
			return []byte("ok"), nil
		},
	}
	if err := b.Build(context.Background(), "/repo", []string{"lt-1"}); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(gotArgs, " ")
	if !strings.Contains(joined, `"lt-1"`) || !strings.Contains(joined, `"lt-1-sb"`) {
		t.Fatalf("variants not expanded: %s", joined)
	}
}

func TestBuildEmptyHostsBuildsWholeSet(t *testing.T) {
	b := &Builder{}
	targets, err := b.targets("/repo", nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(targets) != 1 || targets[0] != "/repo#nixosConfigurations" {
		t.Fatalf("targets = %v, want the bare flake ref", targets)
	}
}

func TestBuildRejectionBecomesValidationError(t *testing.T) {
	b := &Builder{run: func(context.Context, string, ...string) ([]byte, error) {
		return []byte("error: build of '/nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-drv.drv' failed\n" +
			"at /nix/store/aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa-drv.drv:1\n"), errors.New("exit 1")
	}}
	err := b.Build(context.Background(), "/repo", []string{"lt-1"})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("want ValidationError, got %T %v", err, err)
	}
	if !strings.Contains(verr.Detail, "failed") {
		t.Errorf("detail lost the reason: %q", verr.Detail)
	}
	if strings.Contains(verr.Detail, "/nix/store") {
		t.Errorf("store noise not stripped: %q", verr.Detail)
	}
}

func TestBuildDefaultTimeout(t *testing.T) {
	var deadline time.Time
	var hasDeadline bool
	b := &Builder{run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
		deadline, hasDeadline = ctx.Deadline()
		return []byte("ok"), nil
	}}
	before := time.Now()
	if err := b.Build(context.Background(), "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if !hasDeadline {
		t.Fatal("no timeout applied")
	}
	// Zero Timeout means 30 minutes; allow slack for test execution time.
	got := deadline.Sub(before)
	if got < 29*time.Minute+50*time.Second || got > 30*time.Minute+10*time.Second {
		t.Fatalf("default timeout = %s, want ~30m", got)
	}
}

func TestBuildCustomTimeout(t *testing.T) {
	var deadline time.Time
	b := &Builder{
		Timeout: 5 * time.Second,
		run: func(ctx context.Context, _ string, _ ...string) ([]byte, error) {
			deadline, _ = ctx.Deadline()
			return []byte("ok"), nil
		},
	}
	before := time.Now()
	if err := b.Build(context.Background(), "/repo", nil); err != nil {
		t.Fatal(err)
	}
	if got := deadline.Sub(before); got < 4*time.Second || got > 6*time.Second {
		t.Fatalf("custom timeout = %s, want ~5s", got)
	}
}

// TestBuildInvalidHostRejectedBeforeRunning proves the host-slug check (a
// nix-injection firewall shared with EvalGate's applyExpr) runs before the
// runner is ever invoked - an attacker-controlled host string must not reach
// a shell-adjacent nix invocation.
func TestBuildInvalidHostRejectedBeforeRunning(t *testing.T) {
	called := false
	b := &Builder{run: func(context.Context, string, ...string) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}}
	err := b.Build(context.Background(), "/repo", []string{`lt-1"; rm -rf /`})
	if err == nil {
		t.Fatal("invalid host accepted")
	}
	if called {
		t.Fatal("runner invoked despite an invalid host")
	}
}
