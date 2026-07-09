package ports

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
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
