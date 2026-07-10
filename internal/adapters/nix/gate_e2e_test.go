package nix

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// gate_e2e_test.go proves the eval gate END TO END against the reference
// overlay (examples/overlay): a valid fleet document forces a real NixOS
// toplevel derivation; an unknown setting key is rejected by the actual
// module system with the key named in the error. This is gate=eval, the
// write path's safety property, exercised for real.

// stageOverlay copies the example overlay into a temp git repo and pins
// the sextant flake input to this checkout (the relative path breaks when
// copied).
func stageOverlay(t *testing.T) string {
	t.Helper()
	root, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	dst := t.TempDir()
	src := filepath.Join(root, "examples", "overlay") + string(os.PathSeparator) + "."
	if out, err := exec.Command("cp", "-r", src, dst).CombinedOutput(); err != nil {
		t.Fatalf("copy overlay: %v\n%s", err, out)
	}
	p := filepath.Join(dst, "flake.nix")
	b, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	b = []byte(strings.ReplaceAll(string(b), "path:../..", "path:"+root))
	if err := os.WriteFile(p, b, 0o644); err != nil {
		t.Fatal(err)
	}
	// Path inputs are unlocked by definition (real overlays lock a
	// git+https sextant input); regenerate the lock for the copy.
	if err := os.Remove(filepath.Join(dst, "flake.lock")); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("nix", "flake", "lock", dst).CombinedOutput(); err != nil {
		t.Fatalf("nix flake lock: %v\n%s", err, out)
	}
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dst}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("add", "-A")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed overlay")
	return dst
}

func TestEvalGateAgainstRealOverlay(t *testing.T) {
	if testing.Short() {
		t.Skip("short mode: full NixOS eval is seconds-slow")
	}
	if _, err := exec.LookPath("nix"); err != nil {
		t.Skip("nix not available")
	}
	dir := stageOverlay(t)
	gate := NewEvalGate()
	gate.Timeout = 4 * time.Minute
	ctx := context.Background()

	// The committed document passes the gate.
	if err := gate.Validate(ctx, dir, []string{"lt-1"}); err != nil {
		t.Fatalf("valid overlay rejected: %v", err)
	}

	// An unknown setting key must be rejected by the real module system,
	// naming the key - the injection firewall's final layer.
	fleetPath := filepath.Join(dir, "fleet.json")
	orig, err := os.ReadFile(fleetPath)
	if err != nil {
		t.Fatal(err)
	}
	bad := strings.Replace(string(orig),
		`"secureboot": true`,
		`"secureboot": true, "apps.bogus": true`, 1)
	if bad == string(orig) {
		t.Fatal("fixture edit did not apply")
	}
	if err := os.WriteFile(fleetPath, []byte(bad), 0o644); err != nil {
		t.Fatal(err)
	}
	// Flakes read from git: stage the edit like the console's repo does.
	if out, err := exec.Command("git", "-C", dir, "add", "fleet.json").CombinedOutput(); err != nil {
		t.Fatalf("git add: %v\n%s", err, out)
	}

	err = gate.Validate(ctx, dir, []string{"lt-1"})
	var verr *ports.ValidationError
	if !errors.As(err, &verr) {
		t.Fatalf("bogus key accepted by the gate: %v", err)
	}
	if !strings.Contains(verr.Detail, "bogus") {
		t.Errorf("rejection does not name the key:\n%s", verr.Detail)
	}
}
