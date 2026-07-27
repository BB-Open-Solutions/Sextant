package main

// Smoke coverage for the wiring in main.go/capabilities.go that does not
// require Postgres, git or nix: the health/metrics-only capability path and
// the fail-fast config path. Anything past a valid config starts a real
// listener and background workers (server.Run blocks until ctx/signal), so
// it is out of scope for a unit-level smoke test.

import (
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/config"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/health"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/logging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/metrics"
)

// env builds a config.Getenv over a fixed map, the same injectable seam
// config_test.go uses - no process environment is touched.
func env(m map[string]string) config.Getenv {
	return func(k string) string { return m[k] }
}

// TestBuildCapabilitiesNoRepo covers the deployment shape used for probes
// and smoke tests: no --repo means no config plane, so buildCapabilities
// must return cleanly with nothing to mount and nothing to release.
func TestBuildCapabilitiesNoRepo(t *testing.T) {
	cfg, err := config.Load(nil, env(nil))
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if cfg.RepoDir != "" {
		t.Fatalf("precondition: RepoDir = %q, want empty", cfg.RepoDir)
	}

	log := logging.New(io.Discard, "text", "info")
	checks := health.New(time.Second)

	caps, cleanup, err := buildCapabilities(context.Background(), cfg, log, checks, metrics.New())
	if err != nil {
		t.Fatalf("buildCapabilities: %v", err)
	}
	if caps != nil {
		t.Errorf("caps = %v, want nil", caps)
	}
	if cleanup != nil {
		t.Errorf("cleanup = %v, want nil", cleanup)
	}
}

// TestRunFailsFastOnInvalidConfig exercises run's flag-parsing/config wiring:
// a config that fails validation must surface a clear error before any
// server, repo or background worker is touched.
func TestRunFailsFastOnInvalidConfig(t *testing.T) {
	err := run([]string{"--log-level", "deafening"}, env(nil))
	if err == nil {
		t.Fatal("run: want error for invalid --log-level, got nil")
	}
	if !strings.Contains(err.Error(), "log-level") {
		t.Errorf("run error = %q, want it to mention log-level", err)
	}
}

// TestRunFailsFastOnWriteWithoutRepo covers a second, distinct validation
// branch reachable purely through flag parsing, so the fail-fast path isn't
// only exercised via the log-level case above.
func TestRunFailsFastOnWriteWithoutRepo(t *testing.T) {
	err := run([]string{"--write"}, env(nil))
	if err == nil {
		t.Fatal("run: want error for --write without --repo, got nil")
	}
	if !strings.Contains(err.Error(), "--repo") {
		t.Errorf("run error = %q, want it to mention --repo", err)
	}
}
