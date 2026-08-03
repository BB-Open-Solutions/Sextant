package app

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
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
	// riskHoldNotified is the newest risk-marked commit the brake has already
	// reported (see RiskHighMarker). In memory on purpose: the hold itself is
	// re-derived from the commit log on every tick, so a restart costs at most
	// one repeated notification - cheaper than persisting bell bookkeeping.
	// Guarded by mu (markRiskNotified), since the auto-start walk runs outside
	// the tick lock.
	riskHoldNotified string
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
		// Distilled, not raw. A nix failure is mostly "building '/nix/store/...'"
		// repeated, with the cause on one line somewhere inside it; a halt whose
		// reason is that noise tells an operator nothing they can act on.
		st.Reason = "release build failed: " + ports.DistillGateError(bs.Detail)
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

const (
	// engineAuthorName attributes every commit the engine makes itself (ring
	// pins, cohort releases). Auto-flow damping keys on it: a run's own pin
	// commits must never read as a reason to start the next run.
	engineAuthorName  = "sextant-rollout"
	engineAuthorEmail = "rollout@sextant"
	// agentAuthorName authors the device agent's writes (clearing an intent a
	// device has acted on) - machine traffic, like the engine's own commits.
	agentAuthorName = "sextant-agent"
)

// engineAuthor is the identity the engine commits under.
func engineAuthor() ports.Author {
	return ports.Author{Name: engineAuthorName, Email: engineAuthorEmail}
}

// isMachineAuthor reports whether a commit came from the control plane
// itself rather than from a person or an integration.
func isMachineAuthor(name string) bool {
	return name == engineAuthorName || name == agentAuthorName
}

// RingBranch names the machine-owned branch a ring group's devices follow.
func RingBranch(group string) string { return "rings/" + group }

// moveRingRef points one ring branch at rev and pushes when changed.
func (s *RolloutService) moveRingRef(ctx context.Context, group, rev string) error {
	changed, err := s.refs.SetRef(ctx, RingBranch(group), rev)
	if err != nil {
		return err
	}
	// Push even when the LOCAL ref already matched: an earlier tick that set
	// the ref and then failed its push (expired credentials) leaves local ==
	// target while the remote still points at the old release - skipping the
	// push here would mark the ring promoted without any device ever being
	// offered it. The force-push is idempotent; sending it every promotion
	// is cheap insurance.
	if err := s.refs.PushRef(ctx, RingBranch(group)); err != nil {
		return err
	}
	if changed {
		s.log.Info("ring branch moved", "branch", RingBranch(group), "rev", rev)
	}
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
		// Converging, not active: a cohort cannot release onto a device that
		// has never checked in - there is nothing installed to release to.
		active := f.ConvergingGroupDevices(g)
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

// autoFlowLogDepth bounds the damping walk. A baseline pin older than this
// many config commits means the fleet is far behind real history, which is
// exactly when a run should start rather than be damped away.
const autoFlowLogDepth = 200

// RiskHighMarker is the tag a writer appends to a commit SUBJECT (after a
// space) when the change it carries is high-risk: a catalog option marked
// riskClass "high", or an integration being switched on or off. The console
// writes it (internal/http/web, riskMarkerFor); the auto-flow walk below reads
// it back off the log and holds the run for a human (design 0012, "Risk
// brake"). It travels through git rather than through memory precisely because
// the reader is a different process lifetime than the writer.
const RiskHighMarker = "[risk:high]"

// maybeAutoStart makes the ladder standing policy (ADR 0012): when the engine
// is idle and HEAD carries commits by someone other than the machines, it
// starts a run to HEAD itself - a merged change reaches the fleet without an
// operator dispatching it. The gate, the test wave, the soaks and the health
// thresholds are untouched; only the manual dispatch disappears.
//
// Damping is the whole difficulty: promotions THEMSELVES commit (ring pins,
// cohort releases), so HEAD always moves past the pins during a run. Without
// the author walk below, each run's own pin commits would look like new work
// and start the next run, forever.
//
// Called without s.mu held: StartWith takes the lock itself.
func (s *RolloutService) maybeAutoStart(ctx context.Context) error {
	if s.refs == nil {
		return nil // no funnel: pins are data-only, nothing flows
	}
	f := s.cfg.Fleet()
	if f.Rollout == nil || len(f.Rollout.Rings) == 0 || !f.Rollout.AutoFlowEnabled() {
		return nil
	}
	st, err := s.store.Get(ctx)
	if err != nil {
		return err
	}
	// Active, paused and halted runs all own the ring branches: a second run
	// would fight the first one's waves, and a halt is an operator's to clear.
	if st != nil && st.Status != rollout.Completed && st.Status != rollout.Cancelled {
		return nil
	}
	head, err := s.refs.Head(ctx)
	if err != nil {
		return err
	}
	baseline, atHead := promotedBaseline(f)
	if baseline == "" {
		return nil // every ring group unpinned: FollowHead already carries HEAD
	}
	if atHead(head) {
		return nil // the fleet already stands on HEAD
	}
	scan, err := s.nonMachineCommitsSince(ctx, baseline)
	if err != nil || !scan.roll {
		return err
	}
	// The risk brake (design 0012): a change the writer marked high-risk never
	// flows on its own. Everything else about the ladder is unchanged - the
	// operator presses the button, which also makes them the person watching
	// it land.
	if scan.riskHash != "" {
		s.holdForRisk(ctx, scan)
		return nil
	}
	if _, err := s.StartWith(ctx, head, StartOpts{}, engineAuthor()); err != nil {
		return err
	}
	s.log.Info("rollout auto-started", "target", head)
	return nil
}

// promotedBaseline reads the revision the fleet was last promoted to: the
// first pinned ring group's pin, in plan order. atHead reports whether EVERY
// pinned ring group already sits on the given revision. An empty baseline
// means no ring group is pinned at all.
func promotedBaseline(f *fleet.Fleet) (baseline string, atHead func(string) bool) {
	var pins []string
	for _, ring := range f.Rollout.Rings {
		for _, name := range ring.GroupList() {
			if g, ok := f.Groups[name]; ok && g.Pin != "" {
				pins = append(pins, g.Pin)
			}
		}
	}
	if len(pins) == 0 {
		return "", func(string) bool { return true }
	}
	return pins[0], func(rev string) bool {
		for _, p := range pins {
			if p != rev {
				return false
			}
		}
		return true
	}
}

// autoFlowScan is what the damping walk found above the promoted baseline:
// whether a run is warranted at all, and the newest commit that warrants one
// but carries the high-risk marker.
type autoFlowScan struct {
	roll                  bool   // a non-machine commit sits above the baseline
	riskHash, riskSubject string // newest such commit carrying RiskHighMarker
}

// nonMachineCommitsSince walks the commits newer than baseline: roll is set
// when any was authored by something other than the control plane itself (the
// damping rule), and the newest of those carrying RiskHighMarker is reported
// so the caller can hold instead of start. Not finding baseline within
// autoFlowLogDepth commits counts as roll: that much history cannot be one
// run's promotion trail. Only non-machine commits are risk-checked: a marker
// is something a WRITER puts on a change, and the engine's own promotion trail
// must never brake the flow it is part of.
func (s *RolloutService) nonMachineCommitsSince(ctx context.Context, baseline string) (autoFlowScan, error) {
	entries, err := s.cfg.AuditLog(ctx, autoFlowLogDepth)
	if err != nil {
		return autoFlowScan{}, err
	}
	// Newest first: the first marked commit seen is the newest one, which is
	// the hash the hold notifies about.
	var scan autoFlowScan
	for _, e := range entries {
		if e.Hash == baseline {
			return scan, nil // reached the pin: nothing of ours above it
		}
		if isMachineAuthor(e.Author) {
			continue
		}
		scan.roll = true
		if scan.riskHash == "" && strings.Contains(e.Subject, RiskHighMarker) {
			scan.riskHash, scan.riskSubject = e.Hash, e.Subject
		}
	}
	// Baseline never seen: the fleet is further behind than the damping window
	// can explain, so a run is warranted whatever the walk found.
	scan.roll = true
	return scan, nil
}

// holdForRisk parks auto-flow behind a high-risk change and tells the owning
// groups about it ONCE per commit: the hold is re-derived on every tick, so an
// unguarded notify would refill the bell every interval until somebody
// dispatched the run. The hold clears by itself once a manual run moves the
// pins past the marked commit - nothing to reset, nothing to remember.
func (s *RolloutService) holdForRisk(ctx context.Context, scan autoFlowScan) {
	if !s.markRiskNotified(scan.riskHash) {
		return
	}
	s.log.Info("rollout auto-start held", "reason", "risk:high", "commit", scan.riskHash)
	emitAll(ctx, s.notifier, s.audience, notify.Notification{
		Kind:  notify.ApprovalNeeded,
		Title: "High-risk change awaits manual rollout",
		Body: fmt.Sprintf("%q is marked high-risk, so it does not flow to the fleet by itself. "+
			"Start its rollout from Updates when you can watch it land.", scan.riskSubject),
		Link: "/updates/rollout",
	})
}

// markRiskNotified records hash as reported and says whether it was new. It
// takes the tick lock around the field only: maybeAutoStart deliberately runs
// without it (StartWith takes it itself), so the flag cannot simply be guarded
// by holding mu across the whole walk.
func (s *RolloutService) markRiskNotified(hash string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.riskHoldNotified == hash {
		return false
	}
	s.riskHoldNotified = hash
	return true
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
	// Groups limits the run to these groups (test wave + one wave holding
	// them). Empty rolls the full ladder.
	Groups []string
	// Expedited shortens every wave's soak to expeditedSoak (delivery-process
	// q6: urgency shortens the soak, NEVER the evidence - the gate, the test
	// wave and the health thresholds all still apply).
	Expedited bool
	// ChangeID/ChangeTitle name the change request this run delivers.
	ChangeID, ChangeTitle string
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
	return s.StartWith(ctx, target, StartOpts{Groups: []string{scope}}, a)
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
	if len(opts.Groups) > 0 {
		f := s.cfg.Fleet()
		for _, g := range opts.Groups {
			if _, ok := f.Groups[g]; !ok {
				return nil, fmt.Errorf("unknown scope group %q", g)
			}
		}
		test := planned[0]
		if len(opts.Groups) == 1 && len(test.GroupList()) == 1 && test.GroupList()[0] == opts.Groups[0] {
			// The scope IS the test group: one wave suffices.
			rings = []rollout.Ring{test}
		} else {
			rings = []rollout.Ring{test, {Groups: opts.Groups, SoakMinutes: 30}}
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
	st.ChangeID, st.ChangeTitle = opts.ChangeID, opts.ChangeTitle
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
		// Same population the wave measures against, so widening cannot aim
		// at devices that can never report.
		total += len(s.cfg.Fleet().ConvergingGroupDevices(g))
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
		author := engineAuthor()
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
		author := engineAuthor()
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
	// Keep the engine's own explanation. Decide produces a precise one every
	// tick and this loop used to drop it, which is why a wedged run looked
	// like a healthy one.
	st.Note(act, now)
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
			// The ladder as standing policy (ADR 0012): an idle engine picks
			// up non-machine commits itself, so nobody has to dispatch a run.
			if err := s.maybeAutoStart(ctx); err != nil && ctx.Err() == nil {
				s.log.Error("rollout auto-start check failed", "err", err)
			}
		}
	}
}

// SystemClock is the production clock.
type SystemClock struct{}

// Now implements ports.Clock.
func (SystemClock) Now() time.Time { return time.Now() }
