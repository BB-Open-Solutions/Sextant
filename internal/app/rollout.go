package app

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// RolloutService drives staged rollouts: on every tick it asks the pure
// engine for the next action and executes it - committing ring pins through
// the gated write transaction, so every promotion is an audited commit.
type RolloutService struct {
	cfg   *ConfigService
	store ports.RolloutStore
	conv  ports.ConvergenceSource
	clock ports.Clock
	log   *slog.Logger
	// refs steers the rings/<group> branches devices follow (ADR 0011);
	// nil disables the funnel (pins remain data-only).
	refs ports.RefUpdater
	// notifier and audience are optional: when set, a completed rollout tells
	// the owning groups the fleet reached its target.
	notifier Notifier
	audience []string
	mu       sync.Mutex // one tick / start / cancel at a time
}

// NewRolloutService wires the rollout engine.
func NewRolloutService(cfg *ConfigService, store ports.RolloutStore,
	conv ports.ConvergenceSource, clock ports.Clock, log *slog.Logger) *RolloutService {
	return &RolloutService{cfg: cfg, store: store, conv: conv, clock: clock, log: log}
}

// WithRefs enables the update funnel: ring branches move on promotion and
// follow HEAD while no run is active.
func (s *RolloutService) WithRefs(refs ports.RefUpdater) *RolloutService {
	s.refs = refs
	return s
}

// WithNotifier makes a completed rollout notify the given groups.
func (s *RolloutService) WithNotifier(n Notifier, audience []string) *RolloutService {
	s.notifier = n
	s.audience = audience
	return s
}

// notifyDone tells the owning groups a rollout reached the whole fleet. Best
// effort: a notifier error never disturbs the engine tick.
func (s *RolloutService) notifyDone(ctx context.Context, target string) {
	if s.notifier == nil {
		return
	}
	short := target
	if len(short) > 12 {
		short = short[:12]
	}
	for _, g := range s.audience {
		_ = s.notifier.Emit(ctx, notify.Notification{
			Audience: g, Kind: notify.RolloutDone,
			Title: "Rollout complete",
			Body:  fmt.Sprintf("Every ring converged on %s. The fleet is on the target revision.", short),
			Link:  "/rollout",
		})
	}
}

// RingBranch names the machine-owned branch a ring group's devices follow.
func RingBranch(group string) string { return "rings/" + group }

// moveRingRef points one ring branch at rev and pushes when changed.
func (s *RolloutService) moveRingRef(ctx context.Context, group, rev string) error {
	changed, err := s.refs.SetRef(ctx, RingBranch(group), rev)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	if err := s.refs.PushRef(ctx, RingBranch(group)); err != nil {
		return err
	}
	s.log.Info("ring branch moved", "branch", RingBranch(group), "rev", rev)
	return nil
}

// releaseCohort marks the next batch of a capped wave's devices into its
// cohort (SetDevicePin), so the generator's ringBranchFor releases them onto
// the ring branch. An uncapped wave is a no-op: its branch move already
// releases the whole group. Devices are chosen in the group's deterministic
// (sorted) order, so the released set is always the same growing prefix.
func (s *RolloutService) releaseCohort(ctx context.Context, ring rollout.Ring, author ports.Author) error {
	if ring.MaxDevices <= 0 {
		return nil
	}
	f := s.cfg.Fleet()
	active := f.ActiveGroupDevices(ring.Group)
	released := len(f.ReleasedGroupDevices(ring.Group))
	next := ring.NextRelease(len(active), released)
	for _, tag := range active[released:next] {
		msg := fmt.Sprintf("rollout: release %s into wave %s cohort", tag, ring.Group)
		if err := s.cfg.Apply(ctx, fleet.SetDevicePin(tag, ring.Group), msg, author, tag); err != nil {
			return fmt.Errorf("cohort release of %s failed: %w", tag, err)
		}
	}
	return nil
}

// FollowHead fast-forwards every UNPINNED ring branch to HEAD, so idle
// rings track main and a fresh commit reaches them without a rollout run.
// Pinned rings stay put: an active or halted rollout holds them in place.
func (s *RolloutService) FollowHead(ctx context.Context) error {
	if s.refs == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	f := s.cfg.Fleet()
	if f.Rollout == nil {
		return nil
	}
	head, err := s.refs.Head(ctx)
	if err != nil {
		return err
	}
	for _, ring := range f.Rollout.Rings {
		g, ok := f.Groups[ring.Group]
		if !ok || g.Pin != "" {
			continue // pinned: the engine owns this ref via promotions
		}
		if err := s.moveRingRef(ctx, ring.Group, head); err != nil {
			return err
		}
	}
	return nil
}

// rings reads the ring plan from the fleet document.
func (s *RolloutService) rings() ([]rollout.Ring, error) {
	f := s.cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) == 0 {
		return nil, fmt.Errorf("no rollout rings configured (fleet.rollout.rings)")
	}
	out := make([]rollout.Ring, 0, len(f.Rollout.Rings))
	for _, r := range f.Rollout.Rings {
		if _, ok := f.Groups[r.Group]; !ok {
			return nil, fmt.Errorf("rollout ring names unknown group %q", r.Group)
		}
		out = append(out, rollout.Ring{
			Group: r.Group, Name: r.Name, SoakMinutes: r.SoakMinutes,
			MinHealthyPercent: r.MinHealthyPercent, RequireApproval: r.RequireApproval,
			MaxDevices: r.MaxDevices,
		})
	}
	return out, nil
}

// Start begins a rollout to the target revision. One run at a time.
func (s *RolloutService) Start(ctx context.Context, target string, _ ports.Author) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("rollout needs a target revision")
	}
	if _, err := s.rings(); err != nil {
		return nil, err
	}
	cur, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cur != nil && cur.Status == rollout.Active {
		return nil, fmt.Errorf("a rollout to %s is already active", cur.Target)
	}
	st := rollout.NewState(target, s.clock.Now())
	return st, s.store.Put(ctx, st)
}

// Status returns the current run (nil when none) plus live ring convergence.
func (s *RolloutService) Status(ctx context.Context) (*rollout.State, []rollout.RingStatus, error) {
	st, err := s.store.Get(ctx)
	if err != nil || st == nil {
		return st, nil, err
	}
	rings, err := s.rings()
	if err != nil {
		return st, nil, nil // plan changed under the run; state still readable
	}
	statuses := make([]rollout.RingStatus, len(rings))
	for i, r := range rings {
		rs, err := s.conv.RingStatus(ctx, r.Group, st.Target)
		if err != nil {
			return st, nil, err
		}
		statuses[i] = rs
	}
	return st, statuses, nil
}

// Cancel stops the active run. Pins already committed stay (config is
// truth); the operator decides how to proceed.
func (s *RolloutService) Cancel(ctx context.Context) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || st.Status != rollout.Active {
		return nil, fmt.Errorf("no active rollout")
	}
	st.Status = rollout.Cancelled
	st.Updated = s.clock.Now()
	return st, s.store.Put(ctx, st)
}

// Approve records operator sign-off for the current wave, releasing a manual
// gate so the next tick promotes the next wave. A no-op if the current wave is
// not actually a manual gate, so the button is always safe to press.
func (s *RolloutService) Approve(ctx context.Context) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || st.Status != rollout.Active {
		return nil, fmt.Errorf("no active rollout")
	}
	st.Normalize()
	st.Approve(s.clock.Now())
	st.Updated = s.clock.Now()
	return st, s.store.Put(ctx, st)
}

// Tick advances the run by at most one action. Safe to call from a timer
// and from the API; the engine is idempotent between observations.
func (s *RolloutService) Tick(ctx context.Context) (*rollout.Action, *rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, nil, err
	}
	if st == nil || st.Status != rollout.Active {
		return nil, st, nil // nothing to do
	}
	st.Normalize() // stored state may have lost empty maps to omitempty
	rings, err := s.rings()
	if err != nil {
		return nil, st, err
	}

	now := s.clock.Now()
	rs := rollout.RingStatus{}
	if _, promoted := st.PromotedAt[st.Ring]; promoted && st.Ring < len(rings) {
		if rs, err = s.conv.RingStatus(ctx, rings[st.Ring].Group, st.Target); err != nil {
			return nil, st, err
		}
		// Cohort accounting (ADR 0013): RingStatus was scoped to the RELEASED
		// cohort (the convergence source lists released devices). Released is
		// that cohort's size; GroupTotal is the whole active group, so Decide
		// knows whether a capped wave still has devices to widen into.
		rs.Released = rs.Total
		rs.GroupTotal = len(s.cfg.Fleet().ActiveGroupDevices(rings[st.Ring].Group))
	}

	act := rollout.Decide(rings, st, rs, now)
	switch act.Kind {
	case rollout.Promote:
		ring := rings[st.Ring]
		msg := fmt.Sprintf("rollout: pin ring %d (%s) to %s", st.Ring, ring.Group, st.Target)
		author := ports.Author{Name: "sextant-rollout", Email: "rollout@sextant"}
		// Group pin: the audit record + the marker FollowHead uses to leave this
		// ring's branch alone while the engine owns it (capped or not).
		if err := s.cfg.Apply(ctx, fleet.SetGroupPin(ring.Group, st.Target), msg, author,
			AffectedHosts(s.cfg.Fleet(), "group:"+ring.Group)...); err != nil {
			return &act, st, fmt.Errorf("pin commit failed: %w", err)
		}
		// Count-capped canary (ADR 0013): release only the first cohort onto
		// the ring; uncapped waves release the whole group via the branch move.
		if err := s.releaseCohort(ctx, ring, author); err != nil {
			return &act, st, err
		}
		// The funnel (ADR 0011): move the ring's branch so its released devices
		// actually receive the target. Pin commit first (audit), then ref.
		if s.refs != nil {
			if err := s.moveRingRef(ctx, ring.Group, st.Target); err != nil {
				return &act, st, fmt.Errorf("ring branch move failed: %w", err)
			}
		}
		st.PromotedAt[st.Ring] = now
	case rollout.WidenCohort:
		// The current cohort is healthy and soaked; release the next batch and
		// restart the soak (the widened cohort must converge again).
		ring := rings[st.Ring]
		author := ports.Author{Name: "sextant-rollout", Email: "rollout@sextant"}
		if err := s.releaseCohort(ctx, ring, author); err != nil {
			return &act, st, err
		}
		delete(st.ConvergedAt, st.Ring)
	case rollout.Wait:
		// Record first full convergence so the soak clock starts.
		if rs.Total > 0 && rs.OnTarget == rs.Total {
			if _, seen := st.ConvergedAt[st.Ring]; !seen {
				st.ConvergedAt[st.Ring] = now
			}
		}
	case rollout.AwaitApproval:
		// The wave is a manual gate: hold here until an operator approves.
		// No state change; the next tick after Approve advances.
	case rollout.Advance:
		st.Ring++
	case rollout.Halt:
		st.Status = rollout.Halted
		st.Reason = act.Reason
	case rollout.Done:
		st.Status = rollout.Completed
		st.Reason = act.Reason
		s.notifyDone(ctx, st.Target)
	}
	st.Updated = now
	if err := s.store.Put(ctx, st); err != nil {
		return &act, st, err
	}
	s.log.Info("rollout tick", "action", string(act.Kind), "reason", act.Reason,
		"ring", st.Ring, "status", string(st.Status))
	return &act, st, nil
}

// Run ticks the engine on an interval until ctx is cancelled. Wire it next
// to the HTTP server so shutdown drains it.
func (s *RolloutService) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, _, err := s.Tick(ctx); err != nil {
				s.log.Error("rollout tick failed", "err", err)
			}
			// Idle rings follow HEAD so ordinary commits still reach
			// their devices between rollout runs.
			if err := s.FollowHead(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("ring follow failed", "err", err)
			}
		}
	}
}

// SystemClock is the production clock.
type SystemClock struct{}

// Now implements ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }
