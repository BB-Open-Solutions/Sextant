package postgres

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

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
//
// The DELETE and every INSERT are queued into one pgx.Batch and sent in a
// single round-trip (SendBatch), rather than one tx.Exec per device. The
// domain caps a report at up to discovery.MaxBatch (4096) devices; on a WAN
// station (~50ms RTT) 4096 sequential round-trips would hold this
// transaction open for minutes, bloating WAL and locks for no benefit - a
// single flush is exactly as correct and orders of magnitude cheaper.
func (d *DiscoveredStore) Report(ctx context.Context, tenant, station string, devices []discovery.Discovered, now time.Time) error {
	tx, err := d.s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	batch := &pgx.Batch{}
	batch.Queue(`DELETE FROM discovered WHERE tenant=$1 AND station=$2`, tenant, station)
	for _, dev := range devices {
		lastSeen := dev.LastSeen
		if lastSeen.IsZero() {
			lastSeen = now // the station may omit it; stamp on receipt
		}
		batch.Queue(`
			INSERT INTO discovered
				(tenant, station, mac, serial, vendor, model, cpu, cores, mem_gb, disk_gb, firmware, facter, phase, last_seen)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14)`,
			tenant, station, dev.MAC, dev.Serial, dev.Vendor, dev.Model, dev.CPU,
			dev.Cores, dev.MemGB, dev.DiskGB, dev.Firmware, facterArg(dev.Facter), string(dev.Phase), lastSeen,
		)
	}

	br := tx.SendBatch(ctx, batch)
	// Results must be consumed in queue order (DELETE first, then each
	// INSERT) even though nothing is read back - Exec surfaces the first
	// failing statement's error, and the batch must be fully drained before
	// Close so the underlying connection can be reused cleanly.
	if _, err := br.Exec(); err != nil {
		_ = br.Close()
		return fmt.Errorf("clear station set: %w", err)
	}
	for i, dev := range devices {
		if _, err := br.Exec(); err != nil {
			_ = br.Close()
			return fmt.Errorf("insert discovery %s (%d/%d): %w", dev.MAC, i+1, len(devices), err)
		}
	}
	if err := br.Close(); err != nil {
		return fmt.Errorf("close discovery batch: %w", err)
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
