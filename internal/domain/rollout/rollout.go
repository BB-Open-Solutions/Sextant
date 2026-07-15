// Package rollout is the pure decision engine for staged rollouts. Rings
// are ordered groups; each ring pins to the target revision, soaks, and must
// converge healthy before the next ring promotes. All I/O (reading
// convergence, committing pins, the clock) lives in the application layer;
// this package only decides the next action.
package rollout

import (
	"fmt"
	"time"
)

// Ring is one wave of the deployment pipeline: a device group plus its
// promotion gates. Early waves are typically test waves (a canary, then a
// broader test group); later waves are production phases.
type Ring struct {
	// Group is the device group this wave pins.
	Group string `json:"group"`
	// Name is an operator-facing label for the wave ("Canary", "Test",
	// "Phase 1"). Empty falls back to the group name in the UI.
	Name string `json:"name,omitempty"`
	// SoakMinutes is the minimum time the wave must run on the target after
	// converging before the next wave may promote.
	SoakMinutes int `json:"soakMinutes,omitempty"`
	// MinHealthyPercent gates promotion of the NEXT wave: at least this
	// share of the wave's devices must be healthy on the target. Zero means
	// 100 (every device healthy).
	MinHealthyPercent int `json:"minHealthyPercent,omitempty"`
	// RequireApproval makes this wave a manual gate: even after it soaks
	// healthy, the pipeline waits for an operator to approve promotion to the
	// next wave (the enterprise "test sign-off" step). Auto-advance otherwise.
	RequireApproval bool `json:"requireApproval,omitempty"`
	// MaxDevices caps how many of the group's devices receive the target at
	// once (a count-capped canary): 0 releases the whole group (the default),
	// N > 0 releases at most N at a time, widening as each cohort converges.
	// See ADR 0013; the engine/generator wiring is a later slice.
	MaxDevices int `json:"maxDevices,omitempty"`
}

// Label is the wave's display name (its Name, or the group if unnamed).
func (r Ring) Label() string {
	if r.Name != "" {
		return r.Name
	}
	return r.Group
}

func (r Ring) minHealthy() int {
	if r.MinHealthyPercent <= 0 {
		return 100
	}
	return r.MinHealthyPercent
}

// Cohort selects the devices released so far in a count-capped wave. devices
// must already be in a stable order (the caller sorts, e.g. by tag) so the
// same machines are chosen deterministically across evaluations. released is
// how many the engine has released; it is clamped to [0, len]. With a zero or
// negative MaxDevices there is no cap and the whole slice is returned. This is
// the pure selection ADR 0013 builds on; the engine grows `released` as each
// cohort converges healthy.
func (r Ring) Cohort(devices []string, released int) []string {
	if r.MaxDevices <= 0 {
		return devices
	}
	if released < 0 {
		released = 0
	}
	if released > len(devices) {
		released = len(devices)
	}
	return devices[:released]
}

// NextRelease is how many devices should be released after widening one
// cohort: the whole group when uncapped, otherwise the current count plus the
// cap, bounded by the group size. Starting from 0 this releases MaxDevices,
// then 2*MaxDevices, and so on until the group is fully released.
func (r Ring) NextRelease(total, released int) int {
	if r.MaxDevices <= 0 {
		return total
	}
	next := released + r.MaxDevices
	if next > total {
		return total
	}
	return next
}

// FullyReleased reports whether every device in the wave's group has been
// released (a capped wave still ends up releasing the whole group, cohort by
// cohort).
func (r Ring) FullyReleased(total, released int) bool { return released >= total }

// RunStatus is the lifecycle of one rollout run.
type RunStatus string

// The lifecycle states a rollout run passes through.
const (
	Active    RunStatus = "active"
	Halted    RunStatus = "halted" // gate failed; needs a human decision
	Completed RunStatus = "completed"
	Cancelled RunStatus = "cancelled"
)

// State is the durable record of one rollout run.
type State struct {
	// Target is the revision every ring converges to.
	Target string `json:"target"`
	// Ring is the index of the ring currently being rolled out.
	Ring int `json:"ring"`
	// PromotedAt records when each ring's pin was committed (keyed by ring
	// index); the soak clock starts at convergence-after-promotion.
	PromotedAt map[int]time.Time `json:"promotedAt,omitempty"`
	// ConvergedAt records when each ring was first observed fully converged
	// on the target; soak counts from here.
	ConvergedAt map[int]time.Time `json:"convergedAt,omitempty"`
	// ApprovedAt records when a manual-gate wave was approved for promotion
	// (keyed by ring index). A wave with RequireApproval waits here until set.
	ApprovedAt map[int]time.Time `json:"approvedAt,omitempty"`
	// BuildRequestedAt records when a ring's release build was requested
	// (keyed by ring index): the promotion is held until the build lands in
	// the binary cache (build-before-promote). Cleared on promotion.
	BuildRequestedAt map[int]time.Time `json:"buildRequestedAt,omitempty"`
	Status           RunStatus         `json:"status"`
	// Reason explains a halt.
	Reason  string    `json:"reason,omitempty"`
	Started time.Time `json:"started"`
	Updated time.Time `json:"updated"`
}

// NewState starts a rollout run at ring 0.
func NewState(target string, now time.Time) *State {
	return &State{
		Target: target, Ring: 0, Status: Active,
		PromotedAt:       map[int]time.Time{},
		ConvergedAt:      map[int]time.Time{},
		ApprovedAt:       map[int]time.Time{},
		BuildRequestedAt: map[int]time.Time{},
		Started:          now, Updated: now,
	}
}

// Approve records operator sign-off for the current wave, releasing a manual
// gate so the pipeline may promote to the next wave.
func (s *State) Approve(now time.Time) {
	if s.ApprovedAt == nil {
		s.ApprovedAt = map[int]time.Time{}
	}
	s.ApprovedAt[s.Ring] = now
}

// Normalize repairs maps lost to JSON omitempty on a round trip through a
// store. Call after loading persisted state.
func (s *State) Normalize() {
	if s.PromotedAt == nil {
		s.PromotedAt = map[int]time.Time{}
	}
	if s.ConvergedAt == nil {
		s.ConvergedAt = map[int]time.Time{}
	}
	if s.ApprovedAt == nil {
		s.ApprovedAt = map[int]time.Time{}
	}
	if s.BuildRequestedAt == nil {
		s.BuildRequestedAt = map[int]time.Time{}
	}
}

// RingStatus is the observed convergence of one ring (from the observed
// plane): device totals for the ring's group on the target revision.
type RingStatus struct {
	Total    int // devices in the ring's CURRENT released cohort
	OnTarget int // cohort devices reporting the target revision
	Healthy  int // cohort devices on target and healthy (checked in recently, no errors)
	// Released and GroupTotal drive a count-capped canary (ADR 0013): Released
	// is how many of the group have been released so far (== Total), GroupTotal
	// the whole group. When a capped wave's cohort is healthy and soaked but
	// Released < GroupTotal, the engine widens the cohort instead of advancing.
	// For an uncapped wave the caller sets both equal to Total, so no widening.
	Released   int
	GroupTotal int
}

// ActionKind is what the engine wants done next.
type ActionKind string

const (
	// Promote commits the current ring's pin to the target revision.
	Promote ActionKind = "promote"
	// Wait means observe and try again later (converging or soaking).
	Wait ActionKind = "wait"
	// Advance moves to the next ring.
	Advance ActionKind = "advance"
	// WidenCohort releases the next batch of a capped wave's group (ADR 0013):
	// the current cohort is healthy and soaked, but more of the group is yet to
	// receive the target. The caller marks more devices and restarts the soak.
	WidenCohort ActionKind = "widen-cohort"
	// Halt stops the run: the health gate failed.
	Halt ActionKind = "halt"
	// AwaitApproval means the wave soaked healthy but is a manual gate: it
	// waits for an operator to approve promotion to the next wave.
	AwaitApproval ActionKind = "await-approval"
	// Done means every ring converged: the run is complete.
	Done ActionKind = "done"
)

// Action is the engine's decision plus its reasoning (surfaced in the UI).
type Action struct {
	Kind   ActionKind
	Reason string
}

// Decide returns the next action for the run. Callers execute the action
// (commit a pin, persist state) and call Decide again on the next tick.
func Decide(rings []Ring, s *State, ringStatus RingStatus, now time.Time) Action {
	if s.Status != Active {
		return Action{Kind: Done, Reason: fmt.Sprintf("run is %s", s.Status)}
	}
	if s.Ring >= len(rings) {
		return Action{Kind: Done, Reason: "all rings rolled out"}
	}
	ring := rings[s.Ring]

	// Not yet promoted: commit the pin first.
	if _, promoted := s.PromotedAt[s.Ring]; !promoted {
		return Action{Kind: Promote, Reason: fmt.Sprintf("pin ring %d (%s) to %s", s.Ring, ring.Group, s.Target)}
	}

	// An empty ring cannot converge; treat as done to avoid wedging the run,
	// but say so.
	if ringStatus.Total == 0 {
		return Action{Kind: Advance, Reason: fmt.Sprintf("ring %d (%s) has no devices", s.Ring, ring.Group)}
	}

	// Health gate: too many unhealthy devices on the target halts the run.
	// Only meaningful once devices started converging.
	if ringStatus.OnTarget > 0 {
		healthyPct := ringStatus.Healthy * 100 / ringStatus.OnTarget
		if healthyPct < ring.minHealthy() {
			return Action{Kind: Halt, Reason: fmt.Sprintf(
				"ring %d (%s): only %d%% of converged devices healthy (gate %d%%)",
				s.Ring, ring.Group, healthyPct, ring.minHealthy())}
		}
	}

	// Still converging?
	if ringStatus.OnTarget < ringStatus.Total {
		return Action{Kind: Wait, Reason: fmt.Sprintf(
			"ring %d (%s): %d/%d on target", s.Ring, ring.Group, ringStatus.OnTarget, ringStatus.Total)}
	}

	// Converged: soak from first full convergence.
	converged, seen := s.ConvergedAt[s.Ring]
	if !seen {
		// The caller records ConvergedAt when it observes this Wait.
		return Action{Kind: Wait, Reason: fmt.Sprintf(
			"ring %d (%s): converged, starting soak", s.Ring, ring.Group)}
	}
	soak := time.Duration(ring.SoakMinutes) * time.Minute
	if now.Sub(converged) < soak {
		return Action{Kind: Wait, Reason: fmt.Sprintf(
			"ring %d (%s): soaking until %s", s.Ring, ring.Group, converged.Add(soak).Format(time.RFC3339))}
	}

	// Capped wave: the current cohort is healthy and soaked. If the group is not
	// fully released yet, widen the cohort (release the next batch) and re-soak,
	// rather than advancing to the next wave.
	if ring.MaxDevices > 0 && ringStatus.Released < ringStatus.GroupTotal {
		return Action{Kind: WidenCohort, Reason: fmt.Sprintf(
			"wave %d (%s): cohort of %d/%d healthy and soaked, releasing the next batch",
			s.Ring, ring.Label(), ringStatus.Released, ringStatus.GroupTotal)}
	}

	if s.Ring == len(rings)-1 {
		return Action{Kind: Done, Reason: "last wave converged and soaked"}
	}
	// Manual gate: a wave marked RequireApproval waits for operator sign-off
	// before promoting the next wave, even though it is healthy and soaked.
	if ring.RequireApproval {
		if _, approved := s.ApprovedAt[s.Ring]; !approved {
			return Action{Kind: AwaitApproval, Reason: fmt.Sprintf(
				"wave %d (%s): healthy and soaked, awaiting approval to promote", s.Ring, ring.Label())}
		}
	}
	return Action{Kind: Advance, Reason: fmt.Sprintf("wave %d (%s) healthy and soaked", s.Ring, ring.Label())}
}
