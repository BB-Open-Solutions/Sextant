package app

import (
	"context"
	"os/exec"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// headOf resolves the test repo's current HEAD - the revision auto-flow is
// expected to roll the fleet to.
func headOf(t *testing.T, dir string) string {
	t.Helper()
	out, err := exec.Command("git", "-C", dir, "rev-parse", "--verify", "-q", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
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
	baseline := headOf(t, dir)
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
			name: "a high-risk commit waits for an operator",
			setup: func(t *testing.T, _ *RolloutService, _ *ConfigService, dir string) {
				commitAs(t, dir, "alice", "settings: update 1 at org "+RiskHighMarker)
			},
		},
		{
			name: "a plain commit above a high-risk one is held too",
			setup: func(t *testing.T, _ *RolloutService, _ *ConfigService, dir string) {
				commitAs(t, dir, "alice", "settings: update 1 at org "+RiskHighMarker)
				commitAs(t, dir, "bob", "settings: update 1 at org")
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
			head := headOf(t, dir)

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

// recordingNotifier captures what the engine emitted, so a test can assert
// both the content of a notification and how often it was sent.
type recordingNotifier struct{ sent []notify.Notification }

// Emit implements Notifier.
func (r *recordingNotifier) Emit(_ context.Context, n notify.Notification) error {
	r.sent = append(r.sent, n)
	return nil
}

// TestRolloutAutoStartRiskBrake follows the brake over several ticks (design
// 0012): a marked commit holds the flow and tells the owners ONCE - the hold
// re-derives from the log every tick, so a missing guard would refill the bell
// forever - and the hold ends by itself once a manual run carries the marked
// commit into the pins.
func TestRolloutAutoStartRiskBrake(t *testing.T) {
	rs, svc, dir := autoFlowStack(t)
	rec := &recordingNotifier{}
	rs.WithNotifier(rec, []string{"owners"})
	ctx := context.Background()

	commitAs(t, dir, "alice", "settings: update 1 at org "+RiskHighMarker)
	marked := headOf(t, dir)

	if err := rs.maybeAutoStart(ctx); err != nil {
		t.Fatalf("maybeAutoStart: %v", err)
	}
	st, _, err := rs.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st != nil {
		t.Fatalf("a high-risk commit started a run by itself: %+v", st)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("notifications = %d, want 1: %+v", len(rec.sent), rec.sent)
	}
	if got := rec.sent[0]; got.Kind != notify.ApprovalNeeded ||
		!strings.Contains(got.Body, "settings: update 1 at org") {
		t.Fatalf("hold notification = %+v", got)
	}

	// Same commit, next tick: still held, still one bell.
	if err := rs.maybeAutoStart(ctx); err != nil {
		t.Fatalf("maybeAutoStart: %v", err)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("the hold re-notified: %d notifications", len(rec.sent))
	}

	// An operator dispatched the run and it delivered: the pins now stand on
	// the marked commit, and ordinary work lands on top. Nothing above the
	// baseline is marked any more, so the fleet flows again.
	for _, g := range []string{"canary", "fleet"} {
		if err := svc.Apply(ctx, fleet.SetGroupPin(g, marked),
			"rollout: pin ring ("+g+") to "+marked, engineAuthor()); err != nil {
			t.Fatal(err)
		}
	}
	commitAs(t, dir, "bob", "settings: update 1 at org")
	head := headOf(t, dir)
	if err := rs.maybeAutoStart(ctx); err != nil {
		t.Fatalf("maybeAutoStart: %v", err)
	}
	st, _, err = rs.Status(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if st == nil || st.Status != rollout.Active || st.Target != head {
		t.Fatalf("the hold did not clear once the pins passed it: %+v (head %s)", st, head)
	}
	if len(rec.sent) != 1 {
		t.Fatalf("notifications = %d after the hold cleared, want 1", len(rec.sent))
	}
}
