package app

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/incident"
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
}

// NewComplianceService wires the compliance view.
func NewComplianceService(cfg *ConfigService, inv *InventoryService, clock ports.Clock) *ComplianceService {
	return &ComplianceService{cfg: cfg, inv: inv, clock: clock}
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

	f := s.cfg.Fleet()
	obs := make([]incident.Observation, 0, len(f.Devices))
	for tag, dev := range f.Devices {
		if dev.State == "retired" {
			continue
		}
		o := incident.Observation{
			Tag:    tag,
			Group:  primaryGroup(dev),
			Target: targetRevision(f, dev),
		}
		if st, ok := byTag[tag]; ok {
			o.Deployed = st.Revision
			o.Online = st.Online
			o.LastSeen = st.LastSeen
			o.Error = st.Error
			o.Ack = st.Ack
		}
		obs = append(obs, o)
	}
	return incident.Detect(obs, s.clock.Now()), nil
}

// primaryGroup is the device's most-specific group (first entry), or "" for an
// ungrouped device (an org-level incident).
func primaryGroup(dev fleet.Device) string {
	if len(dev.Groups) > 0 {
		return dev.Groups[0]
	}
	return ""
}

// targetRevision is the pin the device is expected to run: the nearest pinned
// group across its memberships (most specific first), or "" when it follows
// HEAD and cannot be judged behind.
func targetRevision(f *fleet.Fleet, dev fleet.Device) string {
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
