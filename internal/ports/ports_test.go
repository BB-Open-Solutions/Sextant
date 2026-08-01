package ports

import "testing"

// A real nix failure is mostly progress noise with the cause on one line
// somewhere inside it. This is the shape that reached a halted rollout on
// 2026-07-31 and told the operator nothing: twelve identical build lines and
// no error.
func TestDistillGateErrorFindsTheCauseInBuildNoise(t *testing.T) {
	detail := "validation rejected: building '/nix/store/aaa.drv'...\n" +
		"building '/nix/store/bbb.drv'...\n" +
		"error: device lt-1: unknown hardware profile 'lenovo-t495'\n" +
		"building '/nix/store/ccc.drv'...\n"
	got := DistillGateError(detail)
	if got != "device lt-1: unknown hardware profile 'lenovo-t495'" {
		t.Fatalf("got %q; the actionable line is the one the operator needs", got)
	}
}

// With no error line at all there is nothing to distil, and inventing a
// summary would be worse than handing over what we have.
func TestDistillGateErrorFallsBackToTheDetail(t *testing.T) {
	if got := DistillGateError("something odd happened"); got != "something odd happened" {
		t.Errorf("got %q, want the detail unchanged", got)
	}
	if got := DistillGateError("   "); got == "" {
		t.Error("empty detail produced an empty message; a rejection must always say something")
	}
}
