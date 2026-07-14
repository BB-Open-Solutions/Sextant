package ports

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// NotifyStore persists in-app notifications and each reader's read state. A
// reader is identified by their IdP subject plus the memberships (group and
// role names) that let them receive audience-addressed notifications.
type NotifyStore interface {
	// Add stores one notification.
	Add(ctx context.Context, n notify.Notification) error
	// ListFor returns the newest notifications a reader should see (direct or
	// by audience), each with its per-reader Read flag, up to limit.
	ListFor(ctx context.Context, tenant, subject string, memberships []string, limit int) ([]notify.Notification, error)
	// UnreadCount is the number of unread notifications for a reader.
	UnreadCount(ctx context.Context, tenant, subject string, memberships []string) (int, error)
	// MarkRead marks one notification read for this reader. subject follows
	// tenant, matching every other method on this interface (ListFor,
	// UnreadCount, MarkAllRead) - all four string args are otherwise
	// interchangeable to the compiler, so a caller or a new store mirroring
	// the wrong shape would transpose id/subject with no build error.
	MarkRead(ctx context.Context, tenant, subject, id string) error
	// MarkAllRead marks every notification the reader can see as read.
	MarkAllRead(ctx context.Context, tenant, subject string, memberships []string) error
}
