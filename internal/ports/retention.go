package ports

import (
	"context"
	"time"
)

// RetentionStore removes records past their retention window (GDPR art.
// 5(1)(e)). Each method returns how many rows it removed, because a sweep
// that cannot say what it did is a sweep nobody can audit - and one that
// silently did nothing looks exactly like one that ran correctly.
type RetentionStore interface {
	// DeleteNotificationsBefore removes notifications created before cutoff.
	DeleteNotificationsBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error)
	// DeleteElevationBefore removes elevation requests created before cutoff.
	DeleteElevationBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error)
	// DeleteSeenUsersBefore removes cached operator identities last seen
	// before cutoff.
	DeleteSeenUsersBefore(ctx context.Context, tenant string, cutoff time.Time) (int, error)
	// DeleteDeviceStatusBefore removes check-in records last seen before
	// cutoff for tags NOT in known. A device that still exists is never
	// swept, however quiet: its silence is the finding.
	DeleteDeviceStatusBefore(ctx context.Context, tenant string, cutoff time.Time, known map[string]bool) (int, error)
}
