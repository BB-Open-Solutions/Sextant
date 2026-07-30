package app

import (
	"context"
	"sort"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// ComplianceService turns the observed plane into incidents: for every live
// device it compares what the device reports running against what its group is
// pinned to, plus liveness and last remote-action outcome, and hands the
// snapshot to the pure detector. The transport layer filters the result to the
// scopes a viewer may see, so an operator responsible for a few groups sees
// only their action items.
type ComplianceService struct {
	cfg   *ConfigService
	inv   *InventoryService
	clock ports.Clock
	// runs is the rollout run, read-only and optional: the stall guard needs
	// the current wave's promotion clock. Narrow on purpose - compliance
	// OBSERVES the rollout, it must never be able to drive it.
	runs ports.RolloutStore
}

// NewComplianceService wires the compliance view.
func NewComplianceService(cfg *ConfigService, inv *InventoryService, clock ports.Clock) *ComplianceService {
	return &ComplianceService{cfg: cfg, inv: inv, clock: clock}
}

// WithRollout lets the compliance view read the current rollout run, so a run
// that has stopped making progress becomes an action item instead of a silent
// wait. Without it the per-device guards still work.
func (s *ComplianceService) WithRollout(store ports.RolloutStore) *ComplianceService {
	s.runs = store
	return s
}

// Incidents returns every current action item across the fleet, most-severe
// first. Retired devices are excluded. Callers scope the result per viewer.
func (s *ComplianceService) Incidents(ctx context.Context) ([]incident.Incident, error) {
	views, err := s.inv.StatusAll(ctx)
	if err != nil {
		return nil, err
	}
	byTag := make(map[string]StatusView, len(views))
	for _, v := range views {
		byTag[v.Tag] = v
	}

	// The repo's own tip, resolved once per call: the unknown-config guard
	// needs it to tell a revision the repo does not know from one it has
	// merely not counted yet.
	head := s.cfg.Head(ctx)
	headRelease := s.cfg.ReleaseNumber(ctx, head)

	f := s.cfg.Fleet()
	obs := make([]incident.Observation, 0, len(f.Devices))
	for tag, dev := range f.Devices {
		if dev.State == "retired" {
			continue
		}
		o := incident.Observation{
			Tag:         tag,
			Group:       primaryGroup(dev),
			Target:      TargetRevision(f, dev),
			Head:        head,
			HeadRelease: headRelease,
		}
		if st, ok := byTag[tag]; ok {
			o.Deployed = st.Revision
			o.Online = st.Online
			o.LastSeen = st.LastSeen
			o.Error = st.Error
			o.Ack = st.Ack
		}
		o.DeployedRelease = s.cfg.ReleaseNumber(ctx, o.Deployed)
		o.TargetRelease = s.cfg.ReleaseNumber(ctx, o.Target)
		// The cores those revisions pin. This is what separates "a setting has
		// not arrived yet" from "this machine runs an older system": the first
		// is a warning, the second becomes an issue once it persists. Both
		// lookups are cached per revision and unknown is left unjudged.
		if core, ok := s.cfg.CoreVersionAt(ctx, o.Deployed); ok {
			o.DeployedCore = core.Rev
		}
		if core, ok := s.cfg.CoreVersionAt(ctx, o.Target); ok {
			o.TargetCore = core.Rev
			o.TargetCorePinned = core.Modified
		}
		obs = append(obs, o)
	}
	now := s.clock.Now()
	out := incident.Detect(obs, now)
	if run := s.runIncidents(ctx, f, obs, now); len(run) > 0 {
		out = append(out, run...)
		incident.Sort(out)
	}
	return out, nil
}

// runIncidents raises the run-level guards for the rollout in flight. It is
// best-effort: an unreachable rollout store must not blank the whole
// compliance view, so a failure degrades to "no run-level incidents" rather
// than an error the caller would render as an empty page.
func (s *ComplianceService) runIncidents(ctx context.Context, f *fleet.Fleet, obs []incident.Observation, now time.Time) []incident.Incident {
	if s.runs == nil {
		return nil
	}
	st, err := s.runs.Get(ctx)
	if err != nil || st == nil {
		return nil
	}
	st.Normalize()
	stalled := st.StalledFor(now, st.Ring)
	if stalled == 0 {
		return nil
	}
	ring, ok := s.currentRing(f, st)
	if !ok {
		return nil // a run whose wave plan vanished cannot name what is stuck
	}
	return incident.DetectRollout(incident.RunObservation{
		Ring:      ring.Label(),
		Target:    st.Target,
		Stalled:   stalled,
		OffTarget: offTargetTags(f, ring, obs, st.Target),
		Since:     st.PromotedAt[st.Ring],
	})
}

// currentRing is the wave the run is on: the run's own snapshot when it
// carries one, the org plan otherwise (runs persisted before snapshots
// landed) - the same fallback the rollout service uses.
func (s *ComplianceService) currentRing(f *fleet.Fleet, st *rollout.State) (rollout.Ring, bool) {
	if st.Ring >= 0 && st.Ring < len(st.Rings) {
		return st.Rings[st.Ring], true
	}
	if f.Rollout == nil || st.Ring < 0 || st.Ring >= len(f.Rollout.Rings) {
		return rollout.Ring{}, false
	}
	r := f.Rollout.Rings[st.Ring]
	return rollout.Ring{Group: r.Group, Groups: r.Groups, Name: r.Name,
		SoakMinutes: r.SoakMinutes, MinHealthyPercent: r.MinHealthyPercent,
		RequireApproval: r.RequireApproval, MaxDevices: r.MaxDevices}, true
}

// offTargetTags names the wave's released devices that are not reporting the
// target - the devices the stalled run is waiting on. Released, not active:
// a count-capped wave only ever offered the target to its cohort, so the rest
// of the group is not late (ADR 0013). Sorted and deduplicated: a device may
// sit in more than one of a wave's groups.
func offTargetTags(f *fleet.Fleet, ring rollout.Ring, obs []incident.Observation, target string) []string {
	deployed := make(map[string]string, len(obs))
	for _, o := range obs {
		deployed[o.Tag] = o.Deployed
	}
	seen := map[string]bool{}
	var out []string
	for _, g := range ring.GroupList() {
		for _, tag := range f.ReleasedGroupDevices(g) {
			if seen[tag] || deployed[tag] == target {
				continue
			}
			seen[tag] = true
			out = append(out, tag)
		}
	}
	sort.Strings(out)
	return out
}

// primaryGroup is the device's most-specific group (first entry), or "" for an
// ungrouped device (an org-level incident).
func primaryGroup(dev fleet.Device) string {
	if len(dev.Groups) > 0 {
		return dev.Groups[0]
	}
	return ""
}

// TargetRevision is the pin the device is expected to run: the nearest pinned
// group across its memberships (most specific first), or "" when it follows
// HEAD and cannot be judged behind. Shared by the incident detector, the
// baseline verdict (design 0008) and the policies coverage counts.
func TargetRevision(f *fleet.Fleet, dev fleet.Device) string {
	for _, g := range dev.Groups {
		chain := f.GroupAncestry(g) // root -> specific
		for i := len(chain) - 1; i >= 0; i-- {
			if grp, ok := f.Groups[chain[i]]; ok && grp.Pin != "" {
				return grp.Pin
			}
		}
	}
	return ""
}
