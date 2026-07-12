package ports

import (
	"context"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
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

// DiscoveredStore persists the pre-enrollment set an imaging station has
// seen, namespaced by tenant and station. A report replaces the station's
// whole set (leases that vanished are gone); enrolling one device removes
// just that MAC. Unlike the config plane this is transient observed state,
// so it lives in Postgres, not git.
type DiscoveredStore interface {
	// Report replaces the station's whole discovered set.
	Report(ctx context.Context, tenant, station string, devices []discovery.Discovered, now time.Time) error
	// List returns a station's current discovered set, MAC-sorted.
	List(ctx context.Context, tenant, station string) ([]discovery.Discovered, error)
	// Remove drops one MAC once it has been enrolled.
	Remove(ctx context.Context, tenant, station, mac string) error
}

// ImageJobStore persists the imaging-execution plane: image jobs an operator
// dispatched for a station, namespaced by tenant and station and keyed by MAC.
// Console-authoritative (unlike the station-replaced discovered set), so a
// discovery report can never clobber a job. Transient observed state, so it
// lives in Postgres, not git.
type ImageJobStore interface {
	// Upsert creates or replaces a job (keyed tenant, station, mac).
	Upsert(ctx context.Context, tenant string, job imaging.Job, now time.Time) error
	// ListByStation returns every job for a station, newest first.
	ListByStation(ctx context.Context, tenant, station string) ([]imaging.Job, error)
	// ListPending returns a station's jobs awaiting or mid-install (the poll).
	ListPending(ctx context.Context, tenant, station string) ([]imaging.Job, error)
	// Get returns one job by MAC, or false.
	Get(ctx context.Context, tenant, station, mac string) (imaging.Job, bool, error)
	// UpdateStatus moves a job to a new status with an optional message.
	UpdateStatus(ctx context.Context, tenant, station, mac string, status imaging.Status, message string, now time.Time) error
	// Delete removes a job (once installed and reconciled, or canceled).
	Delete(ctx context.Context, tenant, station, mac string) error
}

// PrefsStore persists per-user presentation preferences, tenant-namespaced.
type PrefsStore interface {
	GetPrefs(ctx context.Context, tenant, subject string) (identity.Preferences, bool, error)
	PutPrefs(ctx context.Context, tenant, subject string, p identity.Preferences, now time.Time) error
}
