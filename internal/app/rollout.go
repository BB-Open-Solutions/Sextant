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
	// builder, when set, enforces build-before-promote: a ring's release is
	// realised into the binary cache before its branch moves, so devices
	// substitute the release instead of each compiling it locally. Nil skips
	// the build gate (devices build locally, as without a cache).
	builder ports.CacheBuilder
	mu      sync.Mutex // one tick / start / cancel at a time
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

// WithCacheBuilder enables build-before-promote: a ring's release is realised
// into the binary cache before its branch moves.
func (s *RolloutService) WithCacheBuilder(b ports.CacheBuilder) *RolloutService {
	s.builder = b
	return s
}

// ensureRingBuilt drives the release build for the ring about to promote.
// Returns ready=true when the promotion may proceed. While the build runs the
// promotion is simply held (the next tick asks again); a failed build halts
// the run - shipping an unbuilt release to devices is exactly what
// build-before-promote exists to prevent.
func (s *RolloutService) ensureRingBuilt(ctx context.Context, st *rollout.State, ring rollout.Ring, now time.Time) bool {
	if s.builder == nil {
		return true
	}
	var hosts []string
	for _, g := range ring.GroupList() {
		hosts = append(hosts, s.cfg.Fleet().ActiveGroupDevices(g)...)
	}
	if len(hosts) == 0 {
		return true // nothing will pull the release; nothing to build
	}
	bs, err := s.builder.EnsureBuilt(ctx, st.Target, hosts)
	if err != nil {
		// Transient (runner unreachable, etc): hold the promotion and retry on
		// the next tick rather than halting a healthy run.
		s.log.Warn("release build check failed; holding promotion", "err", err)
		return false
	}
	switch bs.Phase {
	case ports.BuildDone:
		delete(st.BuildRequestedAt, st.Ring)
		return true
	case ports.BuildFailed:
		st.Status = rollout.Halted
		st.Reason = "release build failed: " + bs.Detail
		return false
	default: // building
		if _, seen := st.BuildRequestedAt[st.Ring]; !seen {
			st.BuildRequestedAt[st.Ring] = now
			s.log.Info("release build started", "ring", st.Ring, "wave", ring.Label(), "target", st.Target)
		}
		return false
	}
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
	for _, g := range ring.GroupList() {
		active := f.ActiveGroupDevices(g)
		released := len(f.ReleasedGroupDevices(g))
		next := ring.NextRelease(len(active), released)
		for _, tag := range active[released:next] {
			msg := fmt.Sprintf("rollout: release %s into wave %s cohort", tag, g)
			if err := s.cfg.Apply(ctx, fleet.SetDevicePin(tag, g), msg, author, tag); err != nil {
				return fmt.Errorf("cohort release of %s failed: %w", tag, err)
			}
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
	// Only between runs: while a rollout is in flight (active, paused or
	// halted) every ring branch belongs to the engine. Following HEAD here
	// would release later waves early - main advances during a run (pin
	// commits, unrelated merges) and unpromoted rings carry no pin yet, so
	// their devices would leapfrog the staging entirely.
	if st, err := s.store.Get(ctx); err != nil {
		return err
	} else if st != nil && (st.Status == rollout.Active || st.Status == rollout.Paused || st.Status == rollout.Halted) {
		return nil
	}
	f := s.cfg.Fleet()
	if f.Rollout == nil {
		return nil
	}
	head, err := s.refs.Head(ctx)
	if err != nil {
		return err
	}
	for _, ring := range f.Rollout.Rings {
		for _, name := range ring.GroupList() {
			g, ok := f.Groups[name]
			if !ok || g.Pin != "" {
				continue // pinned: the engine owns this ref via promotions
			}
			if err := s.moveRingRef(ctx, name, head); err != nil {
				return err
			}
		}
	}
	return nil
}

// runRings is the wave plan of a run: its own snapshot when it carries one
// (every run started since snapshots landed), the fleet plan otherwise
// (older persisted runs).
func (s *RolloutService) runRings(st *rollout.State) ([]rollout.Ring, error) {
	if st != nil && len(st.Rings) > 0 {
		return st.Rings, nil
	}
	return s.planRings()
}

// planRings reads the configured ring plan from the fleet document.
func (s *RolloutService) planRings() ([]rollout.Ring, error) {
	f := s.cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) == 0 {
		return nil, fmt.Errorf("no rollout rings configured (fleet.rollout.rings)")
	}
	out := make([]rollout.Ring, 0, len(f.Rollout.Rings))
	for _, r := range f.Rollout.Rings {
		for _, g := range r.GroupList() {
			if _, ok := f.Groups[g]; !ok {
				return nil, fmt.Errorf("rollout ring names unknown group %q", g)
			}
		}
		out = append(out, rollout.Ring{
			Group: r.Group, Groups: r.Groups, Name: r.Name, SoakMinutes: r.SoakMinutes,
			MinHealthyPercent: r.MinHealthyPercent, RequireApproval: r.RequireApproval,
			MaxDevices: r.MaxDevices,
		})
	}
	return out, nil
}

// Start begins a rollout to the target revision. One run at a time.
func (s *RolloutService) Start(ctx context.Context, target string, a ports.Author) (*rollout.State, error) {
	return s.StartWith(ctx, target, StartOpts{}, a)
}

// StartOpts tunes one run without touching the org plan.
type StartOpts struct {
	// Scope limits the run to one group (test wave + that group).
	Scope string
	// Expedited shortens every wave's soak to expeditedSoak (delivery-process
	// q6: urgency shortens the soak, NEVER the evidence - the gate, the test
	// wave and the health thresholds all still apply).
	Expedited bool
}

// expeditedSoak is the per-wave soak of an expedited run: long enough for a
// broken release to start failing health checks, short enough for a security
// fix to cross the fleet within the hour.
const expeditedSoak = 5

// StartScoped begins a rollout limited to one group: the plan's test wave
// first (structural, delivery-process q4), then just the scoped group. An
// empty scope rolls the full ladder. Either way the run SNAPSHOTS its wave
// plan into the state, so editing the org plan mid-run cannot reshuffle a
// rollout in flight.
func (s *RolloutService) StartScoped(ctx context.Context, target, scope string, a ports.Author) (*rollout.State, error) {
	return s.StartWith(ctx, target, StartOpts{Scope: scope}, a)
}

// StartWith begins a run shaped by opts; see StartOpts.
func (s *RolloutService) StartWith(ctx context.Context, target string, opts StartOpts, _ ports.Author) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if target == "" {
		return nil, fmt.Errorf("rollout needs a target revision")
	}
	planned, err := s.planRings()
	if err != nil {
		return nil, err
	}
	rings := planned
	if opts.Scope != "" {
		f := s.cfg.Fleet()
		if _, ok := f.Groups[opts.Scope]; !ok {
			return nil, fmt.Errorf("unknown scope group %q", opts.Scope)
		}
		test := planned[0]
		if len(test.GroupList()) == 1 && test.GroupList()[0] == opts.Scope {
			// The scope IS the test group: one wave suffices.
			rings = []rollout.Ring{test}
		} else {
			rings = []rollout.Ring{test, {Groups: []string{opts.Scope}, SoakMinutes: 30}}
		}
	}
	if opts.Expedited {
		short := make([]rollout.Ring, len(rings))
		copy(short, rings)
		for i := range short {
			if short[i].SoakMinutes == 0 || short[i].SoakMinutes > expeditedSoak {
				short[i].SoakMinutes = expeditedSoak
			}
		}
		rings = short
	}
	cur, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if cur != nil && cur.Status == rollout.Active {
		return nil, fmt.Errorf("a rollout to %s is already active", cur.Target)
	}
	st := rollout.NewState(target, s.clock.Now())
	st.Rings = rings
	return st, s.store.Put(ctx, st)
}

// Status returns the current run (nil when none) plus live ring convergence.
func (s *RolloutService) Status(ctx context.Context) (*rollout.State, []rollout.RingStatus, error) {
	st, err := s.store.Get(ctx)
	if err != nil || st == nil {
		return st, nil, err
	}
	rings, err := s.runRings(st)
	if err != nil {
		//nolint:nilerr // deliberate degrade: an old run without a snapshot
		// whose fleet plan disappeared cannot compute ring convergence, but
		// the run's own state is still valid and must render rather than 500.
		return st, nil, nil
	}
	statuses := make([]rollout.RingStatus, len(rings))
	for i, r := range rings {
		rs, err := s.conv.RingStatus(ctx, r.GroupList(), st.Target)
		if err != nil {
			return st, nil, err
		}
		if _, promoted := st.PromotedAt[i]; promoted {
			rs = s.cohortFixup(rs, r)
		}
		statuses[i] = rs
	}
	return st, statuses, nil
}

// cohortFixup applies the same cohort accounting Tick uses (ADR 0013):
// conv.RingStatus scopes Total to the RELEASED cohort only (the convergence
// source lists released devices), so Released is that cohort's size and
// GroupTotal is the whole active group - otherwise a count-capped wave that
// released only part of its group reads as "N/N on target" with no group
// total, looking complete when it is not.
func (s *RolloutService) cohortFixup(rs rollout.RingStatus, ring rollout.Ring) rollout.RingStatus {
	rs.Released = rs.Total
	total := 0
	for _, g := range ring.GroupList() {
		total += len(s.cfg.Fleet().ActiveGroupDevices(g))
	}
	rs.GroupTotal = total
	return rs
}

// Cancel stops a run that is not finished yet - active, paused OR halted:
// a halted run is exactly the one an operator wants to clear before rolling
// out a fixed release. Pins already committed stay (config is truth); the
// operator decides how to proceed.
func (s *RolloutService) Cancel(ctx context.Context) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || (st.Status != rollout.Active && st.Status != rollout.Paused && st.Status != rollout.Halted) {
		return nil, fmt.Errorf("no rollout to cancel")
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
	rings, err := s.runRings(st)
	if err != nil {
		return nil, st, err
	}

	now := s.clock.Now()
	rs := rollout.RingStatus{}
	if _, promoted := st.PromotedAt[st.Ring]; promoted && st.Ring < len(rings) {
		if rs, err = s.conv.RingStatus(ctx, rings[st.Ring].GroupList(), st.Target); err != nil {
			return nil, st, err
		}
		// Cohort accounting (ADR 0013), so Decide knows whether a capped wave
		// still has devices to widen into.
		rs = s.cohortFixup(rs, rings[st.Ring])
	}

	act := rollout.Decide(rings, st, rs, now)
	switch act.Kind {
	case rollout.Promote:
		ring := rings[st.Ring]
		// Build-before-promote: the release must be in the binary cache before
		// the ring's branch moves, so devices substitute instead of compiling.
		if !s.ensureRingBuilt(ctx, st, ring, now) {
			st.Updated = now
			if err := s.store.Put(ctx, st); err != nil {
				return &act, st, err
			}
			s.log.Info("rollout tick", "action", "await-build", "ring", st.Ring,
				"status", string(st.Status))
			return &act, st, nil
		}
		author := ports.Author{Name: "sextant-rollout", Email: "rollout@sextant"}
		// Group pin: the audit record + the marker FollowHead uses to leave this
		// ring's branch alone while the engine owns it (capped or not).
		for _, g := range ring.GroupList() {
			msg := fmt.Sprintf("rollout: pin ring %d (%s) to %s", st.Ring, g, st.Target)
			if err := s.cfg.Apply(ctx, fleet.SetGroupPin(g, st.Target), msg, author,
				AffectedHosts(s.cfg.Fleet(), "group:"+g)...); err != nil {
				return &act, st, fmt.Errorf("pin commit failed: %w", err)
			}
		}
		// Count-capped canary (ADR 0013): release only the first cohort onto
		// the ring; uncapped waves release the whole group via the branch move.
		if err := s.releaseCohort(ctx, ring, author); err != nil {
			return &act, st, err
		}
		// The funnel (ADR 0011): move the ring's branch so its released devices
		// actually receive the target. Pin commit first (audit), then ref.
		if s.refs != nil {
			for _, g := range ring.GroupList() {
				if err := s.moveRingRef(ctx, g, st.Target); err != nil {
					return &act, st, fmt.Errorf("ring branch move failed: %w", err)
				}
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
		// Record the first threshold-met convergence so the soak clock starts.
		if st.Ring < len(rings) && rings[st.Ring].Converged(rs) {
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

// stragglerSource is the optional extension of ConvergenceSource that can
// name the devices behind a wave's percentages. The postgres adapter has it;
// test fakes need not.
type stragglerSource interface {
	RingStragglers(ctx context.Context, groups []string, target string) ([]rollout.Straggler, error)
}

// Stragglers names the devices keeping a group's wave under 100% for the
// given target. Empty (never an error) when the convergence source cannot
// break its numbers down per device.
func (s *RolloutService) Stragglers(ctx context.Context, groups []string, target string) []rollout.Straggler {
	src, ok := s.conv.(stragglerSource)
	if !ok {
		return nil
	}
	out, err := src.RingStragglers(ctx, groups, target)
	if err != nil {
		s.log.Warn("straggler lookup failed", "groups", groups, "err", err)
		return nil
	}
	return out
}

// Pause freezes an active run (delivery-process §7.6): the engine skips a
// non-active run entirely, so nothing promotes, widens or moves branches
// until Resume. Distinct from a halt: no failure, just an operator's hold.
func (s *RolloutService) Pause(ctx context.Context) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || st.Status != rollout.Active {
		return st, fmt.Errorf("no active rollout to pause")
	}
	st.Status = rollout.Paused
	st.Reason = "paused by operator"
	st.Updated = s.clock.Now()
	return st, s.store.Put(ctx, st)
}

// Resume lifts an operator pause.
func (s *RolloutService) Resume(ctx context.Context) (*rollout.State, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.store.Get(ctx)
	if err != nil {
		return nil, err
	}
	if st == nil || st.Status != rollout.Paused {
		return st, fmt.Errorf("no paused rollout to resume")
	}
	st.Status = rollout.Active
	st.Reason = ""
	st.Updated = s.clock.Now()
	return st, s.store.Put(ctx, st)
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
