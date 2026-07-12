package app

import (
	"context"
	"fmt"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// DiscoveryService is the application surface of the pre-enrollment plane: an
// imaging station reports what it has seen, the console lists it, and enrolling
// a device drops it from the set. Validation lives in the domain; this service
// wires it to the store for one tenant namespace.
type DiscoveryService struct {
	store  ports.DiscoveredStore
	clock  ports.Clock
	tenant string
}

// NewDiscoveryService wires the pre-enrollment plane for one tenant namespace.
func NewDiscoveryService(store ports.DiscoveredStore, clock ports.Clock, tenant string) *DiscoveryService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &DiscoveryService{store: store, clock: clock, tenant: tenant}
}

// Report replaces a station's whole discovered set after validating it. A
// malformed report is rejected in full rather than partially applied, so a
// broken station cannot leave the set half-updated.
func (d *DiscoveryService) Report(ctx context.Context, station string, r discovery.Report) error {
	if station == "" {
		return fmt.Errorf("report needs a station tag")
	}
	if err := r.Validate(); err != nil {
		return err
	}
	return d.store.Report(ctx, d.tenant, station, r.Devices, d.clock.Now())
}

// List returns a station's current discovered set.
func (d *DiscoveryService) List(ctx context.Context, station string) ([]discovery.Discovered, error) {
	return d.store.List(ctx, d.tenant, station)
}

// Get returns one discovered device by MAC, or false if the station has not
// (or no longer) seen it - the enroll path resolves the chosen MAC through it.
func (d *DiscoveryService) Get(ctx context.Context, station, mac string) (discovery.Discovered, bool, error) {
	mac = discovery.NormalizeMAC(mac)
	list, err := d.store.List(ctx, d.tenant, station)
	if err != nil {
		return discovery.Discovered{}, false, err
	}
	for _, dev := range list {
		if dev.MAC == mac {
			return dev, true, nil
		}
	}
	return discovery.Discovered{}, false, nil
}

// Remove drops one MAC once it has been enrolled.
func (d *DiscoveryService) Remove(ctx context.Context, station, mac string) error {
	return d.store.Remove(ctx, d.tenant, station, discovery.NormalizeMAC(mac))
}
