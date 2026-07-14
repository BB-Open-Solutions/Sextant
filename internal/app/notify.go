package app

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// MailSender delivers a notification by e-mail. Implemented by MailService;
// optional, so an instance with no SMTP configured still notifies in-app.
type MailSender interface {
	SendTo(ctx context.Context, to []string, subject, body string) error
}

// NotifyService emits and reads in-app notifications. Emitters (the change
// flow, the rollout engine) call Emit with the audience and content; the
// service stamps the identity fields and persists. Readers page their inbox
// and mark items read. It is the one place a fleet event becomes a message.
type NotifyService struct {
	store  ports.NotifyStore
	clock  ports.Clock
	tenant string

	// Optional e-mail delivery: when both are set, an emitted notification is
	// also mailed to the resolved recipients. mailer sends; dir turns a
	// recipient subject or an audience group into e-mail addresses; consoleURL
	// prefixes the notification's link so the mail is clickable.
	mailer     MailSender
	dir        ports.UserDirectory
	consoleURL string
}

// NewNotifyService wires the service to a store, clock and tenant.
func NewNotifyService(store ports.NotifyStore, clock ports.Clock, tenant string) *NotifyService {
	return &NotifyService{store: store, clock: clock, tenant: tenant}
}

// WithMail enables e-mail delivery of notifications. consoleURL (may be empty)
// is the base the notification link is joined onto for a clickable message.
func (s *NotifyService) WithMail(mailer MailSender, dir ports.UserDirectory, consoleURL string) *NotifyService {
	s.mailer = mailer
	s.dir = dir
	s.consoleURL = strings.TrimRight(consoleURL, "/")
	return s
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
	if err := s.store.Add(ctx, n); err != nil {
		return err
	}
	s.deliverEmail(n)
	return nil
}

// deliverEmail resolves a notification's audience to e-mail addresses and
// sends it, off the caller's goroutine so an SMTP stall never holds the change
// or rollout lock the emitter runs under. Entirely best-effort: no mailer, no
// directory, no recipients, or a send error all end quietly - the in-app
// notification is the source of truth.
func (s *NotifyService) deliverEmail(n notify.Notification) {
	if s.mailer == nil || s.dir == nil {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = s.mailNotification(ctx, n)
	}()
}

// mailNotification resolves a notification's audience to addresses and sends
// it. Split from the goroutine so it is testable synchronously. Returns nil
// when there is nobody to mail (not an error).
func (s *NotifyService) mailNotification(ctx context.Context, n notify.Notification) error {
	var to []string
	if n.Recipient != "" {
		if email, ok, err := s.dir.EmailForSubject(ctx, s.tenant, n.Recipient); err != nil {
			return err
		} else if ok {
			to = []string{email}
		}
	} else {
		emails, err := s.dir.EmailsForAudience(ctx, s.tenant, n.Audience)
		if err != nil {
			return err
		}
		to = emails
	}
	if len(to) == 0 {
		return nil
	}
	body := n.Body
	if n.Link != "" && s.consoleURL != "" {
		body += "\n\n" + s.consoleURL + n.Link
	}
	return s.mailer.SendTo(ctx, to, n.Title, body)
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
	return s.store.MarkRead(ctx, s.tenant, subject, id)
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
