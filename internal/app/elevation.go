package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// ElevationService carries a request from a device to an operator and the
// answer back (#27). It decides; it never grants - the device turns an
// approval into an authentication through PAM, or does not. See the elevation
// package comment for why that split is what makes this safe.
type ElevationService struct {
	store  ports.ElevationStore
	clock  ports.Clock
	tenant string

	// notifier and approvers are optional, exactly as in the change flow: with
	// them set, a raised request reaches the people who can answer it instead
	// of waiting to be noticed.
	notifier  Notifier
	approvers []string
}

// NewElevationService wires the queue.
func NewElevationService(store ports.ElevationStore, clock ports.Clock, tenant string) *ElevationService {
	return &ElevationService{store: store, clock: clock, tenant: tenant}
}

// WithNotifier tells the approver groups when a request is raised. approvers
// are the audiences that receive it - the same groups the change flow asks to
// review, because answering an elevation is the same authority.
func (s *ElevationService) WithNotifier(n Notifier, approvers []string) *ElevationService {
	s.notifier = n
	s.approvers = approvers
	return s
}

// Raise records a device's ask and returns it. The tag comes from the caller's
// authenticated device identity, never from a request body: a device that
// could name another device could get somebody else's request approved and
// then claim the answer.
func (s *ElevationService) Raise(ctx context.Context, tag, user, action, reason string) (elevation.Request, error) {
	id, err := newElevationID()
	if err != nil {
		return elevation.Request{}, err
	}
	r := elevation.Request{
		ID:      id,
		Tag:     strings.TrimSpace(tag),
		User:    strings.TrimSpace(user),
		Action:  trimTo(action, 200),
		Reason:  trimTo(reason, 500),
		State:   elevation.Pending,
		Created: s.clock.Now(),
	}
	if err := r.Valid(); err != nil {
		return elevation.Request{}, err
	}
	if err := s.store.Put(ctx, s.tenant, r); err != nil {
		return elevation.Request{}, err
	}
	s.announce(ctx, r)
	return r, nil
}

// announce tells the approver groups that somebody is waiting. Best-effort by
// construction: the device is holding a dialog open on a five-minute clock, so
// a notification store or an SMTP server having a bad day must not turn into a
// failed request. The queue at /elevation remains the source of truth; this is
// the nudge toward it.
//
// The subject line carries the user, the machine and what they are trying to
// do, because an operator decides on those three and a message that says only
// "a request is waiting" costs a page load to become useful.
func (s *ElevationService) announce(ctx context.Context, r elevation.Request) {
	if s.notifier == nil {
		return
	}
	for _, g := range s.approvers {
		_ = s.notifier.Emit(ctx, notify.Notification{
			Audience: g, Kind: notify.ElevationRequested,
			Title: fmt.Sprintf("%s needs approval on %s", r.User, r.Tag),
			Body: fmt.Sprintf("%s (%s). Expires in %s.",
				r.Action, reasonOrNone(r.Reason), elevation.TTL),
			Link: "/elevation",
		})
	}
}

// reasonOrNone keeps the body a sentence when a device sent no reason: an
// empty pair of brackets reads like something went missing.
func reasonOrNone(reason string) string {
	if strings.TrimSpace(reason) == "" {
		return "no reason given"
	}
	return reason
}

// Poll is what the waiting device asks, repeatedly. It returns the request
// with its state resolved for right now, so an expired one reads as expired
// even if nothing has written to it since it was created.
//
// The tag is checked rather than trusted: a device may only poll its own
// requests. Without that, the id - which travels through a user's session -
// would be enough for any device to read another's queue.
func (s *ElevationService) Poll(ctx context.Context, tag, id string) (elevation.Request, bool, error) {
	r, ok, err := s.store.Get(ctx, s.tenant, id)
	if err != nil || !ok || r.Tag != tag {
		return elevation.Request{}, false, err
	}
	r.State = r.Resolve(s.clock.Now())
	return r, true, nil
}

// Pending is the operator's queue: the requests still waiting for an answer,
// oldest first, because somebody is standing in front of each of them and the
// one who has waited longest is closest to giving up.
func (s *ElevationService) Pending(ctx context.Context) ([]elevation.Request, error) {
	all, err := s.store.Pending(ctx, s.tenant)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]elevation.Request, 0, len(all))
	for _, r := range all {
		// Filter here rather than trusting the store's idea of "pending":
		// expiry is a function of the clock, so a row written five minutes ago
		// is stale without anybody having touched it.
		if r.Resolve(now) == elevation.Pending {
			out = append(out, r)
		}
	}
	return out, nil
}

// Decide answers a request. The approver's name is recorded and the domain
// refuses a second answer, so two operators clicking at once cannot produce
// two verdicts.
func (s *ElevationService) Decide(ctx context.Context, id string, approve bool, by string) (elevation.Request, error) {
	r, ok, err := s.store.Get(ctx, s.tenant, id)
	if err != nil {
		return elevation.Request{}, err
	}
	if !ok {
		return elevation.Request{}, fmt.Errorf("no such elevation request")
	}
	decided, err := r.Decide(approve, by, s.clock.Now())
	if err != nil {
		return elevation.Request{}, err
	}
	if err := s.store.Put(ctx, s.tenant, decided); err != nil {
		return elevation.Request{}, err
	}
	return decided, nil
}

// newElevationID is 128 bits of randomness. The id is the only thing tying a
// waiting device to its answer, so it must not be guessable: an id somebody
// could predict would let them poll for - and consume - an approval meant for
// another user.
func newElevationID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate elevation id: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// trimTo bounds a free-text field a device supplies. Both fields are shown to
// an operator, and a device that can send a megabyte of text can make the
// approval queue unreadable for everyone else.
func trimTo(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

// Now is the service's clock, so a page can render "waited 40s" against the
// same instant the queue was filtered with. A page reading the wall clock
// instead could show a request as still open that Pending had already dropped.
func (s *ElevationService) Now() time.Time { return s.clock.Now() }
