package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// NotifyService emits and reads in-app notifications. Emitters (the change
// flow, the rollout engine) call Emit with the audience and content; the
// service stamps the identity fields and persists. Readers page their inbox
// and mark items read. It is the one place a fleet event becomes a message.
type NotifyService struct {
	store  ports.NotifyStore
	clock  ports.Clock
	tenant string
}

// NewNotifyService wires the service to a store, clock and tenant.
func NewNotifyService(store ports.NotifyStore, clock ports.Clock, tenant string) *NotifyService {
	return &NotifyService{store: store, clock: clock, tenant: tenant}
}

// Emit stamps id, tenant and time on a caller-built notification, validates
// it, and stores it. The caller sets exactly one of Recipient or Audience
// plus Kind and Title. A validation error is returned; a store error is
// returned too so a caller that cares can log it, but emitters treat
// notification delivery as best-effort and ignore it.
func (s *NotifyService) Emit(ctx context.Context, n notify.Notification) error {
	n.ID = newNotifyID()
	n.Tenant = s.tenant
	n.CreatedAt = s.clock.Now().UTC()
	if err := n.Validate(); err != nil {
		return err
	}
	return s.store.Add(ctx, n)
}

// List returns the reader's newest notifications, each with its read flag.
func (s *NotifyService) List(ctx context.Context, subject string, memberships []string, limit int) ([]notify.Notification, error) {
	return s.store.ListFor(ctx, s.tenant, subject, memberships, limit)
}

// Unread is the reader's unread count, for the bell badge.
func (s *NotifyService) Unread(ctx context.Context, subject string, memberships []string) (int, error) {
	return s.store.UnreadCount(ctx, s.tenant, subject, memberships)
}

// MarkRead marks one notification read for the reader.
func (s *NotifyService) MarkRead(ctx context.Context, id, subject string) error {
	return s.store.MarkRead(ctx, s.tenant, id, subject)
}

// MarkAllRead marks every notification the reader can see as read.
func (s *NotifyService) MarkAllRead(ctx context.Context, subject string, memberships []string) error {
	return s.store.MarkAllRead(ctx, s.tenant, subject, memberships)
}

// newNotifyID returns a random opaque id. Notifications are addressed by
// recipient/audience, never by a guessable id, so a random 128-bit value is
// enough to avoid collisions without leaking ordering.
func newNotifyID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
