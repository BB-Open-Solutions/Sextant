package postgres

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// Discovered exposes the pre-enrollment store on the pool.
func (s *Store) Discovered() *DiscoveredStore { return &DiscoveredStore{s} }

// DiscoveredStore implements ports.DiscoveredStore.
type DiscoveredStore struct{ s *Store }

// Report replaces the station's whole discovered set in one transaction, so a
// concurrent List never sees a half-applied report and vanished leases are
// gone atomically.
func (d *DiscoveredStore) Report(ctx context.Context, tenant, station string, devices []discovery.Discovered, now time.Time) error {
	tx, err := d.s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	if _, err := tx.Exec(ctx,
		`DELETE FROM discovered WHERE tenant=$1 AND station=$2`, tenant, station); err != nil {
		return fmt.Errorf("clear station set: %w", err)
	}
	for _, dev := range devices {
		lastSeen := dev.LastSeen
		if lastSeen.IsZero() {
			lastSeen = now // the station may omit it; stamp on receipt
		}
		if _, err := tx.Exec(ctx, `
			INSERT INTO discovered
				(tenant, station, mac, serial, vendor, model, cpu, cores, mem_gb, disk_gb, firmware, facter, phase, last_seen)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			tenant, station, dev.MAC, dev.Serial, dev.Vendor, dev.Model, dev.CPU,
			dev.Cores, dev.MemGB, dev.DiskGB, dev.Firmware, facterArg(dev.Facter), string(dev.Phase), lastSeen,
		); err != nil {
			return fmt.Errorf("insert discovery %s: %w", dev.MAC, err)
		}
	}
	return tx.Commit(ctx)
}

// List returns a station's current discovered set, MAC-sorted.
func (d *DiscoveredStore) List(ctx context.Context, tenant, station string) ([]discovery.Discovered, error) {
	rows, err := d.s.pool.Query(ctx, `
		SELECT mac, serial, vendor, model, cpu, cores, mem_gb, disk_gb, firmware, facter, phase, last_seen
		FROM discovered WHERE tenant=$1 AND station=$2 ORDER BY mac`, tenant, station)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []discovery.Discovered
	for rows.Next() {
		var dev discovery.Discovered
		var facter []byte
		var phase string
		if err := rows.Scan(&dev.MAC, &dev.Serial, &dev.Vendor, &dev.Model, &dev.CPU,
			&dev.Cores, &dev.MemGB, &dev.DiskGB, &dev.Firmware, &facter, &phase, &dev.LastSeen); err != nil {
			return nil, err
		}
		dev.Facter = string(facter)
		dev.Phase = observed.Phase(phase)
		out = append(out, dev)
	}
	return out, rows.Err()
}

// Remove drops one MAC once it has been enrolled.
func (d *DiscoveredStore) Remove(ctx context.Context, tenant, station, mac string) error {
	_, err := d.s.pool.Exec(ctx,
		`DELETE FROM discovered WHERE tenant=$1 AND station=$2 AND mac=$3`, tenant, station, mac)
	return err
}

// facterArg passes an empty facter document as SQL NULL rather than an empty
// jsonb, keeping "no document" distinct from "empty document".
func facterArg(facter string) any {
	if facter == "" {
		return nil
	}
	return []byte(facter)
}
