package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// TestFunnelMovesRingBranches: the update funnel (ADR 0011) end to end
// against a real git repo - promotion moves the ring's branch to the
// target, FollowHead fast-forwards only unpinned rings.
func TestFunnelMovesRingBranches(t *testing.T) {
	rs, svc, conv, clock, dir := newRolloutStack(t)
	ctx := context.Background()
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	rs.WithRefs(repo)
	rev := func(ref string) string {
		out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", ref).Output()
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(out))
	}
	head := rev("HEAD")

	// Idle: FollowHead creates both ring branches at HEAD.
	if err := rs.FollowHead(ctx); err != nil {
		t.Fatal(err)
	}
	if rev("rings/canary") != head || rev("rings/fleet") != head {
		t.Fatalf("idle follow: canary=%s fleet=%s head=%s",
			rev("rings/canary"), rev("rings/fleet"), head)
	}

	// Start a rollout to HEAD (a real revision in this repo) and promote
	// ring 0: the pin commit lands AND the canary branch points at target.
	if _, err := rs.Start(ctx, head, ports.Author{}); err != nil {
		t.Fatal(err)
	}
	if got := tick(t, rs); got != rollout.Promote {
		t.Fatalf("tick = %s, want promote", got)
	}
	if svc.Fleet().Groups["canary"].Pin != head {
		t.Fatal("pin not committed")
	}
	if rev("rings/canary") != head {
		t.Fatal("canary branch not moved to target")
	}

	// A new commit on main: FollowHead moves ONLY the unpinned fleet ring.
	shr := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	shr("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "--allow-empty", "-m", "next")

	// During the run, FollowHead is a no-op: main advanced (a pin commit,
	// an unrelated merge) but the unpromoted fleet ring must NOT leapfrog
	// its wave by following HEAD.
	if err := rs.FollowHead(ctx); err != nil {
		t.Fatal(err)
	}
	if rev("rings/fleet") != head {
		t.Fatalf("unpromoted ring left its position during the run: %s", rev("rings/fleet"))
	}
	if rev("rings/canary") != head {
		t.Fatal("pinned ring moved off its target")
	}

	// Once the run is out of the way, idle rings follow HEAD again.
	if _, err := rs.Cancel(ctx); err != nil {
		t.Fatal(err)
	}
	// Cancel keeps pins (config is truth); clear the canary pin so the ring
	// counts as idle again.
	if err := svc.Apply(ctx, fleet.SetGroupPin("canary", ""), "unpin", ports.Author{}); err != nil {
		t.Fatal(err)
	}
	// The unpin itself is a commit, so HEAD advanced again.
	newHead := rev("HEAD")
	if err := rs.FollowHead(ctx); err != nil {
		t.Fatal(err)
	}
	if rev("rings/fleet") != newHead || rev("rings/canary") != newHead {
		t.Fatalf("idle rings did not follow HEAD after the run: fleet=%s canary=%s want %s",
			rev("rings/fleet"), rev("rings/canary"), newHead)
	}
	_ = conv
	_ = clock
}
