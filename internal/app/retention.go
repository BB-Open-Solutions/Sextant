package app

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// retention.go: storage limitation for the personal data the observed plane
// accumulates (GDPR art. 5(1)(e)).
//
// WHY THIS EXISTS. Measured on 2026-08-07 while writing the processing
// register: of ten processings, exactly ONE had a retention window - the
// diagnostics bundles, which expire after fourteen days. Operator
// identities, elevation requests, check-ins and notifications all grew
// without bound. "We never delete" is not a decision anybody made; it is
// what happens when nobody writes the sweep.
//
// The diagnostics window is enforced on read, which works there because an
// expired bundle is only interesting when somebody asks for it. It does not
// work here: nobody reads a two-year-old notification, so nothing would ever
// trigger. This is a sweeper.
//
// THE NUMBERS BELOW ARE DEFAULTS, NOT LAW. Retention is the controller's
// decision - the municipality's, not the product's - and a tool that picks
// silently is a tool that decided for them. They are deliberately generous:
// too long is a documented risk, too short destroys an audit trail somebody
// needed. Every window is configurable, and the sweep says what it removed
// on every run so the choice stays visible.
//
// NOT swept here, on purpose:
//   - the git history: that IS the audit trail, and its retention is an
//     organisational decision with its own legal basis;
//   - device_secrets: LUKS recovery keys live as long as the device does,
//     and are revoked with it;
//   - user_prefs: one row per operator, no behavioural content.

// RetentionPolicy is how long each kind of record is kept. A zero window
// disables the sweep for that kind - the caller has to ask for deletion.
type RetentionPolicy struct {
	// Notifications: operational messages telling an operator something
	// needs review. They name people and what they did.
	Notifications time.Duration
	// Elevation: who asked for which privileged action on which machine,
	// why, and who decided. The most personal record in the observed plane.
	Elevation time.Duration
	// SeenUsers: the cached identity of console operators (subject, e-mail,
	// name, groups). Somebody who left the organisation should age out.
	SeenUsers time.Duration
	// DeviceStatus: check-ins for tags that have not reported in this long
	// AND are not in the fleet document. A device that still exists is never
	// swept however quiet it is - silence is a finding, not a reason to
	// forget it.
	DeviceStatus time.Duration
}

// DefaultRetention is what a deployment gets without a decision. Generous on
// purpose: see the note above.
func DefaultRetention() RetentionPolicy {
	return RetentionPolicy{
		Notifications: 180 * 24 * time.Hour, // half a year
		Elevation:     365 * 24 * time.Hour, // a year: it is evidence of a decision
		SeenUsers:     365 * 24 * time.Hour,
		DeviceStatus:  180 * 24 * time.Hour,
	}
}

// RetentionSweeper removes records past their window.
type RetentionSweeper struct {
	store  ports.RetentionStore
	policy RetentionPolicy
	tenant string
	clock  ports.Clock
	log    *slog.Logger
	// known reports the tags currently in the fleet document. A device that
	// exists is never swept; only records for tags the fleet has forgotten.
	// nil means "cannot tell", and then device status is left alone entirely
	// rather than guessed at.
	known func() map[string]bool
}

// NewRetentionSweeper wires the sweeper.
func NewRetentionSweeper(store ports.RetentionStore, policy RetentionPolicy,
	tenant string, clock ports.Clock, log *slog.Logger) *RetentionSweeper {
	return &RetentionSweeper{store: store, policy: policy, tenant: tenant, clock: clock, log: log}
}

// WithFleet supplies the live tag set so device status is only swept for
// devices the fleet no longer has.
func (s *RetentionSweeper) WithFleet(known func() map[string]bool) *RetentionSweeper {
	s.known = known
	return s
}

// RetentionResult is what one sweep removed.
type RetentionResult struct {
	Notifications int
	Elevation     int
	SeenUsers     int
	DeviceStatus  int
}

// Total is the sum, for the "did anything happen" log line.
func (r RetentionResult) Total() int {
	return r.Notifications + r.Elevation + r.SeenUsers + r.DeviceStatus
}

// Sweep removes everything past its window and reports what went.
//
// Each kind is independent: a failure on one is logged and the rest still
// run. A sweeper that gives up on the first error would leave three
// categories growing because of a problem in the fourth.
func (s *RetentionSweeper) Sweep(ctx context.Context) (RetentionResult, error) {
	var res RetentionResult
	now := s.clock.Now()
	var firstErr error
	record := func(kind string, err error) {
		if err == nil {
			return
		}
		s.logger().Warn("retention sweep failed for one kind", "kind", kind, "err", err)
		if firstErr == nil {
			firstErr = fmt.Errorf("retention sweep (%s): %w", kind, err)
		}
	}

	if d := s.policy.Notifications; d > 0 {
		n, err := s.store.DeleteNotificationsBefore(ctx, s.tenant, now.Add(-d))
		record("notifications", err)
		res.Notifications = n
	}
	if d := s.policy.Elevation; d > 0 {
		n, err := s.store.DeleteElevationBefore(ctx, s.tenant, now.Add(-d))
		record("elevation", err)
		res.Elevation = n
	}
	if d := s.policy.SeenUsers; d > 0 {
		n, err := s.store.DeleteSeenUsersBefore(ctx, s.tenant, now.Add(-d))
		record("seen users", err)
		res.SeenUsers = n
	}
	// Device status only for tags the fleet has forgotten. Without a fleet
	// source this is skipped rather than guessed: deleting the check-in
	// history of a live device would erase the evidence that it is silent,
	// which is the one thing that record is for.
	if d := s.policy.DeviceStatus; d > 0 && s.known != nil {
		known := s.known()
		if len(known) == 0 {
			// Same refusal as the credential sweep: a document that failed to
			// load must not read as "the fleet is empty".
			s.logger().Warn("retention: refusing to sweep device status against an empty fleet document")
		} else {
			n, err := s.store.DeleteDeviceStatusBefore(ctx, s.tenant, now.Add(-d), known)
			record("device status", err)
			res.DeviceStatus = n
		}
	}

	// Logged EVERY run, including a run that removed nothing. "Deleted
	// nothing" and "never ran" must not look the same - that distinction
	// cost a whole release when the credential sweep sat behind a nil guard
	// and said nothing at all.
	s.logger().Info("retention sweep",
		"removed", res.Total(),
		"notifications", res.Notifications,
		"elevation", res.Elevation,
		"seenUsers", res.SeenUsers,
		"deviceStatus", res.DeviceStatus)
	return res, firstErr
}

// Run sweeps on an interval until ctx is cancelled. Daily is enough: these
// are windows of months, and a sweep that runs hourly only adds load.
func (s *RetentionSweeper) Run(ctx context.Context, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.logger().Error("retention sweep failed", "err", err)
			}
		}
	}
}

func (s *RetentionSweeper) logger() *slog.Logger {
	if s.log != nil {
		return s.log
	}
	return slog.Default()
}
