package ports

import (
	"context"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/rollout"
)

// ChangeStore persists change requests durably: a restart loses nothing.
// The Tier-0 adapter is a locked file store; Postgres replaces it for HA.
type ChangeStore interface {
	List(ctx context.Context) ([]change.CR, error)
	Get(ctx context.Context, id string) (change.CR, bool, error)
	Put(ctx context.Context, cr change.CR) error
}

// RolloutStore persists the current rollout run (nil when none is active).
type RolloutStore interface {
	Get(ctx context.Context) (*rollout.State, error)
	Put(ctx context.Context, s *rollout.State) error
}

// ConvergenceSource reports a ring's observed convergence on a target
// revision. The observed plane (device check-ins) implements it; until that
// plane exists a source may report zero data, which the engine treats as
// "no devices".
type ConvergenceSource interface {
	RingStatus(ctx context.Context, group, target string) (rollout.RingStatus, error)
}

// StatusStore persists device check-ins, namespaced by tenant. Upserts are
// the hot path (every device every minute); implementations batch and
// index accordingly.
type StatusStore interface {
	// Upsert records a check-in observed at now.
	Upsert(ctx context.Context, tenant string, c observed.CheckIn, now time.Time) error
	// Get returns one device's observed state.
	Get(ctx context.Context, tenant, tag string) (observed.DeviceStatus, bool, error)
	// List returns every device's observed state for a tenant, tag-sorted.
	List(ctx context.Context, tenant string) ([]observed.DeviceStatus, error)
	// Ping reports store reachability (deep readiness).
	Ping(ctx context.Context) error
}

// InventoryStore persists device hardware facts (nixos-facter reports),
// merge-on-write so a light check-in never clobbers rich data.
type InventoryStore interface {
	PutFacts(ctx context.Context, tenant, tag string, facts []byte, now time.Time) error
	GetFacts(ctx context.Context, tenant, tag string) ([]byte, time.Time, bool, error)
}
