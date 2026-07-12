package app

import (
	"context"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// memDiscoveredStore is an in-memory ports.DiscoveredStore for service tests.
type memDiscoveredStore struct {
	sets map[string][]discovery.Discovered // key: tenant|station
}

func newMemDiscoveredStore() *memDiscoveredStore {
	return &memDiscoveredStore{sets: map[string][]discovery.Discovered{}}
}

func (m *memDiscoveredStore) key(tenant, station string) string { return tenant + "|" + station }

func (m *memDiscoveredStore) Report(_ context.Context, tenant, station string, devices []discovery.Discovered, now time.Time) error {
	cp := make([]discovery.Discovered, len(devices))
	copy(cp, devices)
	for i := range cp {
		if cp[i].LastSeen.IsZero() {
			cp[i].LastSeen = now
		}
	}
	m.sets[m.key(tenant, station)] = cp
	return nil
}

func (m *memDiscoveredStore) List(_ context.Context, tenant, station string) ([]discovery.Discovered, error) {
	return m.sets[m.key(tenant, station)], nil
}

func (m *memDiscoveredStore) Remove(_ context.Context, tenant, station, mac string) error {
	set := m.sets[m.key(tenant, station)]
	out := set[:0:0]
	for _, d := range set {
		if d.MAC != mac {
			out = append(out, d)
		}
	}
	m.sets[m.key(tenant, station)] = out
	return nil
}

func TestDiscoveryServiceReportValidatesAndReplaces(t *testing.T) {
	svc := NewDiscoveryService(newMemDiscoveredStore(), newFakeClock(testT0), "")
	ctx := context.Background()

	// A malformed report is rejected in full.
	bad := discovery.Report{Devices: []discovery.Discovered{{MAC: "nope", Phase: observed.Discovered}}}
	if err := svc.Report(ctx, "nuc-1", bad); err == nil {
		t.Fatal("malformed report accepted")
	}
	// An empty station tag is rejected.
	if err := svc.Report(ctx, "", discovery.Report{}); err == nil {
		t.Fatal("empty station tag accepted")
	}

	first := discovery.Report{Devices: []discovery.Discovered{
		{MAC: "aa:bb:cc:dd:ee:01", Phase: observed.Discovered},
		{MAC: "aa:bb:cc:dd:ee:02", Phase: observed.Installing},
	}}
	if err := svc.Report(ctx, "nuc-1", first); err != nil {
		t.Fatal(err)
	}
	got, _ := svc.List(ctx, "nuc-1")
	if len(got) != 2 {
		t.Fatalf("want 2 discovered, got %d", len(got))
	}
	if got[0].LastSeen.IsZero() {
		t.Fatal("service did not stamp LastSeen on receipt")
	}

	// A later report REPLACES the set - a vanished lease is gone.
	second := discovery.Report{Devices: []discovery.Discovered{
		{MAC: "aa:bb:cc:dd:ee:01", Phase: observed.Installed},
	}}
	if err := svc.Report(ctx, "nuc-1", second); err != nil {
		t.Fatal(err)
	}
	got, _ = svc.List(ctx, "nuc-1")
	if len(got) != 1 || got[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("replace failed: %+v", got)
	}
}

func TestDiscoveryServiceGetNormalizesAndRemove(t *testing.T) {
	svc := NewDiscoveryService(newMemDiscoveredStore(), newFakeClock(testT0), "")
	ctx := context.Background()

	_ = svc.Report(ctx, "nuc-1", discovery.Report{Devices: []discovery.Discovered{
		{MAC: "aa:bb:cc:dd:ee:01", Vendor: "Lenovo", Phase: observed.Discovered},
	}})

	// Get accepts an upper-case MAC and finds the normalised entry.
	dev, ok, err := svc.Get(ctx, "nuc-1", "AA:BB:CC:DD:EE:01")
	if err != nil || !ok || dev.Vendor != "Lenovo" {
		t.Fatalf("get = %+v %v %v", dev, ok, err)
	}
	if _, ok, _ := svc.Get(ctx, "nuc-1", "aa:bb:cc:dd:ee:99"); ok {
		t.Fatal("get found a MAC that was never reported")
	}

	// Enrolling drops just that MAC.
	if err := svc.Remove(ctx, "nuc-1", "AA:BB:CC:DD:EE:01"); err != nil {
		t.Fatal(err)
	}
	if got, _ := svc.List(ctx, "nuc-1"); len(got) != 0 {
		t.Fatalf("remove left %d entries", len(got))
	}
}
