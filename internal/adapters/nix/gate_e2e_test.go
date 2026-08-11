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

// stageSource exports HEAD into a temp directory and returns it, so the
// sextant flake input points at something that cannot move.
//
// Pointing it at the live checkout is what it used to do, and it made this
// test fail for reasons that had nothing to do with it. `nix flake lock`
// records the narHash of a path: input when it locks; the eval that follows
// re-reads it. Anything that writes inside the repository in between - a
// parallel test run, a commit, a second session, an editor saving a file -
// changes that hash, and nix reports
//
//	error: NAR hash mismatch in input 'path:/.../DAWO-Sextant'
//
// attached to whatever attribute it was evaluating, which reads as a broken
// overlay. Measured 2026-08-11: a push hook and an interactive `go test` ran
// together and this test was the only casualty.
//
// HEAD rather than the working tree is also the more honest subject: the
// gate's job is to judge what would be pushed, and CI evaluates a commit.
func stageSource(t *testing.T, root string) string {
	t.Helper()
	dst := t.TempDir()
	// git archive writes a tar of the committed tree; no .git, no build
	// artefacts, no untracked scratch - all of which would otherwise be
	// hashed into the input and none of which the flake needs.
	cmd := exec.Command("sh", "-c",
		"git -C "+root+" archive --format=tar HEAD | tar -x -C "+dst)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("export HEAD: %v\n%s", err, out)
	}
	return dst
}

// stageOverlay copies the example overlay into a temp git repo and pins
// the sextant flake input to an immutable export of this checkout (the
// relative path breaks when copied, and the live path moves).
func stageOverlay(t *testing.T) string {
	t.Helper()
	repo, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	root := stageSource(t, repo)
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
