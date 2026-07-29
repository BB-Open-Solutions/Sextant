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

// revOf resolves a revision in the test repo.
func revOf(t *testing.T, dir, ref string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", ref).Output()
	if err != nil {
		t.Fatalf("rev-parse %s: %v", ref, err)
	}
	return strings.TrimSpace(string(out))
}

// commitAs adds one config commit under the given author name - the only
// thing auto-flow damping judges a commit by.
func commitAs(t *testing.T, dir, author, msg string) {
	t.Helper()
	out, err := exec.Command("git", "-C", dir,
		"-c", "user.name="+author, "-c", "user.email="+author+"@example.org",
		"commit", "-q", "--allow-empty", "-m", msg).CombinedOutput()
	if err != nil {
		t.Fatalf("commit as %s: %v\n%s", author, err, out)
	}
}

// autoFlowStack is an idle engine after a completed run: refs wired and every
// ring group pinned (by the engine, as a real promotion does) to a baseline
// commit that is genuinely in the repo's history.
func autoFlowStack(t *testing.T) (*RolloutService, *ConfigService, string) {
	t.Helper()
	rs, svc, _, _, dir := newRolloutStack(t)
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	rs.WithRefs(repo)
	baseline := revOf(t, dir, "HEAD")
	for _, g := range []string{"canary", "fleet"} {
		if err := svc.Apply(context.Background(), fleet.SetGroupPin(g, baseline),
			"rollout: pin ring ("+g+") to "+baseline, engineAuthor()); err != nil {
			t.Fatal(err)
		}
	}
	return rs, svc, dir
}

// TestRolloutAutoStart covers the standing-policy engine (ADR 0012): an idle
// engine rolls HEAD out itself when a human put something there, and stays
// put in every case where a run would be wrong - most importantly when the
// only commits above the pins are the engine's own promotion trail, which
// would otherwise make every run start the next one forever.
func TestRolloutAutoStart(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, rs *RolloutService, svc *ConfigService, dir string)
		want  bool
	}{
		{
			name: "human commit above the pins flows to the fleet",
			setup: func(t *testing.T, _ *RolloutService, _ *ConfigService, dir string) {
				commitAs(t, dir, "alice", "settings: turn on disk encryption")
			},
			want: true,
		},
		{
			name:  "the engine's own pin commits do not start a run",
			setup: func(*testing.T, *RolloutService, *ConfigService, string) {},
		},
		{
			name: "agent and engine commits together do not start a run",
			setup: func(t *testing.T, _ *RolloutService, _ *ConfigService, dir string) {
				commitAs(t, dir, agentAuthorName, "intent: clear c-1")
				commitAs(t, dir, engineAuthorName, "rollout: release c-1 into wave canary cohort")
			},
		},
		{
			name: "a run already in flight is never joined by a second",
			setup: func(t *testing.T, rs *RolloutService, _ *ConfigService, dir string) {
				commitAs(t, dir, "alice", "settings: turn on disk encryption")
				if _, err := rs.Start(context.Background(), "rev-operator", ports.Author{}); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "autoFlow false returns the org to manual dispatch",
			setup: func(t *testing.T, _ *RolloutService, svc *ConfigService, dir string) {
				off := false
				plan := *svc.Fleet().Rollout
				plan.AutoFlow = &off
				if err := svc.Apply(context.Background(), fleet.SetRolloutPlan(&plan),
					"rollout: manual dispatch only", engineAuthor()); err != nil {
					t.Fatal(err)
				}
				commitAs(t, dir, "alice", "settings: turn on disk encryption")
			},
		},
		{
			name: "unpinned rings need no run: they follow HEAD already",
			setup: func(t *testing.T, _ *RolloutService, svc *ConfigService, dir string) {
				for _, g := range []string{"canary", "fleet"} {
					if err := svc.Apply(context.Background(), fleet.SetGroupPin(g, ""),
						"rollout: unpin "+g, engineAuthor()); err != nil {
						t.Fatal(err)
					}
				}
				commitAs(t, dir, "alice", "settings: turn on disk encryption")
			},
		},
		{
			name: "a baseline beyond the damping window starts a run anyway",
			setup: func(t *testing.T, _ *RolloutService, svc *ConfigService, _ string) {
				for _, g := range []string{"canary", "fleet"} {
					if err := svc.Apply(context.Background(), fleet.SetGroupPin(g, "rev-ancient"),
						"rollout: pin ring ("+g+") to rev-ancient", engineAuthor()); err != nil {
						t.Fatal(err)
					}
				}
			},
			want: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rs, svc, dir := autoFlowStack(t)
			tc.setup(t, rs, svc, dir)
			ctx := context.Background()
			head := revOf(t, dir, "HEAD")

			if err := rs.maybeAutoStart(ctx); err != nil {
				t.Fatalf("maybeAutoStart: %v", err)
			}

			st, _, err := rs.Status(ctx)
			if err != nil {
				t.Fatal(err)
			}
			started := st != nil && st.Status == rollout.Active && st.Target == head
			if started != tc.want {
				t.Fatalf("auto-started = %v, want %v (state %+v, head %s)", started, tc.want, st, head)
			}
		})
	}
}
