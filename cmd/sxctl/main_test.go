package main

import (
	"bytes"
	"strings"
	"testing"
)

// runFor is a small test helper: it clears both credential env vars first
// (t.Setenv scopes the change to this test and restores it after), sets the
// ones the caller supplies, and runs sxctl with fresh stdout/stderr buffers.
// Only stderr is returned - every test in this file asserts on the exit
// code and the diagnostic message, never on stdout.
func runFor(t *testing.T, env map[string]string, args ...string) (code int, stderr string) {
	t.Helper()
	t.Setenv("SEXTANT_URL", "")
	t.Setenv("SEXTANT_TOKEN", "")
	for k, v := range env {
		t.Setenv(k, v)
	}
	var outBuf, errBuf bytes.Buffer
	code = run(args, &outBuf, &errBuf)
	return code, errBuf.String()
}

// TestRunNoArguments covers the bare "sxctl" invocation: no resource given,
// so it must print usage and fail with exit code 2 - before even looking at
// SEXTANT_URL/SEXTANT_TOKEN, since there is nothing to dispatch yet.
func TestRunNoArguments(t *testing.T) {
	code, stderr := runFor(t, nil)
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "sxctl - Sextant fleet CLI") {
		t.Errorf("stderr = %q, want usage banner", stderr)
	}
}

// TestRunFlagParseError covers an unrecognized flag: flag.ContinueOnError
// makes fs.Parse fail without panicking, and run must translate that into
// exit code 2 (a usage-shaped failure), not a crash or exit code 1.
func TestRunFlagParseError(t *testing.T) {
	code, stderr := runFor(t, nil, "-not-a-real-flag", "devices", "list")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if stderr == "" {
		t.Error("stderr = \"\", want flag parse error output")
	}
}

// TestRunMissingCredentials covers the required-config gate: with neither
// SEXTANT_URL nor SEXTANT_TOKEN set (and no -url flag), run must fail fast
// with exit code 2 and a clear message, never attempting a request.
func TestRunMissingCredentials(t *testing.T) {
	code, stderr := runFor(t, nil, "devices", "list")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, "SEXTANT_URL and SEXTANT_TOKEN are required") {
		t.Errorf("stderr = %q, want the missing-credentials message", stderr)
	}
}

// TestRunUnknownResource covers dispatch's default case: a resource name
// that matches none of the known subcommands must surface as a usageError
// (exit code 2, message naming the bad resource), not exit code 1 or a
// silent no-op. Credentials are set so the failure is unambiguously about
// dispatch, not the earlier required-config gate.
func TestRunUnknownResource(t *testing.T) {
	code, stderr := runFor(t, map[string]string{
		"SEXTANT_URL": "http://127.0.0.1:0", "SEXTANT_TOKEN": "t",
	}, "bogus")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `unknown resource "bogus"`) {
		t.Errorf("stderr = %q, want it to name the unknown resource", stderr)
	}
}

// TestRunUnknownVerb covers the same usage-error contract one level down:
// a known resource with an unrecognized verb also resolves to exit code 2,
// via the same *usageError path as an unknown resource.
func TestRunUnknownVerb(t *testing.T) {
	code, stderr := runFor(t, map[string]string{
		"SEXTANT_URL": "http://127.0.0.1:0", "SEXTANT_TOKEN": "t",
	}, "devices", "bogus-verb")
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr, `devices: unknown verb "bogus-verb"`) {
		t.Errorf("stderr = %q, want it to name the unknown verb", stderr)
	}
}
