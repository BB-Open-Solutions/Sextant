package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// DefaultTenant names the single-tenant namespace until multi-tenant
// routing lands (phase 5+); the storage schema is tenant-ready today.
const DefaultTenant = "default"

// maxFactsBytes bounds a facter report (raw nixos-facter JSON).
const maxFactsBytes = 256 << 10

// InventoryService is the observed plane: device check-ins, status reads
// and hardware facts.
type InventoryService struct {
	status ports.StatusStore
	facts  ports.InventoryStore
	clock  ports.Clock
	tenant string
}

// NewInventoryService wires the observed plane for one tenant namespace.
func NewInventoryService(status ports.StatusStore, facts ports.InventoryStore, clock ports.Clock, tenant string) *InventoryService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &InventoryService{status: status, facts: facts, clock: clock, tenant: tenant}
}

// CheckIn records one device report; facts, when present, must be valid
// JSON and within bounds.
func (s *InventoryService) CheckIn(ctx context.Context, c observed.CheckIn, facts []byte) error {
	if err := c.Validate(); err != nil {
		return err
	}
	now := s.clock.Now()
	if err := s.status.Upsert(ctx, s.tenant, c, now); err != nil {
		return err
	}
	if len(facts) > 0 {
		if len(facts) > maxFactsBytes {
			return fmt.Errorf("facts report exceeds %d bytes", maxFactsBytes)
		}
		if !json.Valid(facts) {
			return fmt.Errorf("facts report is not valid JSON")
		}
		return s.facts.PutFacts(ctx, s.tenant, c.Tag, facts, now)
	}
	return nil
}

// StatusView is a device's observed state plus the derived online flag.
type StatusView struct {
	observed.DeviceStatus
	Online bool `json:"online"`
}

// Status returns one device's observed state.
func (s *InventoryService) Status(ctx context.Context, tag string) (StatusView, bool, error) {
	st, ok, err := s.status.Get(ctx, s.tenant, tag)
	if err != nil || !ok {
		return StatusView{}, ok, err
	}
	return StatusView{DeviceStatus: st, Online: st.Online(s.clock.Now())}, true, nil
}

// StatusAll returns every device's observed state, tag-sorted.
func (s *InventoryService) StatusAll(ctx context.Context) ([]StatusView, error) {
	sts, err := s.status.List(ctx, s.tenant)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]StatusView, 0, len(sts))
	for _, st := range sts {
		out = append(out, StatusView{DeviceStatus: st, Online: st.Online(now)})
	}
	return out, nil
}

// Facts returns a device's stored hardware report.
func (s *InventoryService) Facts(ctx context.Context, tag string) (json.RawMessage, time.Time, bool, error) {
	b, at, ok, err := s.facts.GetFacts(ctx, s.tenant, tag)
	return b, at, ok, err
}
