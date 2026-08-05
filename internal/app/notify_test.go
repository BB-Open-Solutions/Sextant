package app

import (
	"context"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// fakeNotifyStore is an in-memory ports.NotifyStore for service tests.
type fakeNotifyStore struct {
	added []notify.Notification
	read  map[string]bool // notifID+subject -> read
}

func newFakeNotifyStore() *fakeNotifyStore {
	return &fakeNotifyStore{read: map[string]bool{}}
}

func (f *fakeNotifyStore) Add(_ context.Context, n notify.Notification) error {
	f.added = append(f.added, n)
	return nil
}

func (f *fakeNotifyStore) ListFor(_ context.Context, tenant, subject string, memberships []string, limit int) ([]notify.Notification, error) {
	var out []notify.Notification
	for _, n := range f.added {
		if n.Tenant == tenant && n.ForReader(subject, memberships) {
			n.Read = f.read[n.ID+subject]
			out = append(out, n)
		}
	}
	return out, nil
}

func (f *fakeNotifyStore) UnreadCount(ctx context.Context, tenant, subject string, memberships []string) (int, error) {
	items, _ := f.ListFor(ctx, tenant, subject, memberships, 0)
	c := 0
	for _, n := range items {
		if !n.Read {
			c++
		}
	}
	return c, nil
}

func (f *fakeNotifyStore) MarkRead(_ context.Context, _, subject, id string) error {
	f.read[id+subject] = true
	return nil
}

func (f *fakeNotifyStore) MarkAllRead(ctx context.Context, tenant, subject string, memberships []string) error {
	items, _ := f.ListFor(ctx, tenant, subject, memberships, 0)
	for _, n := range items {
		f.read[n.ID+subject] = true
	}
	return nil
}

func TestNotifyServiceEmitStampsAndValidates(t *testing.T) {
	store := newFakeNotifyStore()
	when := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
	svc := NewNotifyService(store, clockAt{when}, "acme")

	// A caller-built notification is stamped with id, tenant and time.
	if err := svc.Emit(context.Background(), notify.Notification{
		Recipient: "user-1", Kind: notify.ChangeMerged, Title: "Merged: x",
	}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(store.added) != 1 {
		t.Fatalf("want 1 stored, got %d", len(store.added))
	}
	got := store.added[0]
	if got.ID == "" || got.Tenant != "acme" || !got.CreatedAt.Equal(when) {
		t.Fatalf("emit did not stamp fields: %+v", got)
	}

	// A notification that fails domain validation is rejected, not stored.
	if err := svc.Emit(context.Background(), notify.Notification{Kind: notify.ChangeMerged}); err == nil {
		t.Fatal("want validation error for notification with no title/recipient")
	}
	if len(store.added) != 1 {
		t.Fatalf("invalid notification was stored: %d", len(store.added))
	}
}

// fakeUserDir is an in-memory ports.UserDirectory for fan-out tests.
type fakeUserDir struct {
	emailBySubject map[string]string
	emailsByGroup  map[string][]string
}

func (f fakeUserDir) RecordUser(context.Context, string, string, string, string, []string) error {
	return nil
}
func (f fakeUserDir) EmailForSubject(_ context.Context, _, subject string) (string, bool, error) {
	e, ok := f.emailBySubject[subject]
	return e, ok, nil
}
func (f fakeUserDir) EmailsForAudience(_ context.Context, _, group string) ([]string, error) {
	return f.emailsByGroup[group], nil
}

type recordingMailer struct {
	to      []string
	subject string
	body    string
	sends   int
}

func (m *recordingMailer) SendTo(_ context.Context, to []string, subject, body string) error {
	m.to, m.subject, m.body, m.sends = to, subject, body, m.sends+1
	return nil
}

func TestNotifyEmailFanOut(t *testing.T) {
	dir := fakeUserDir{
		emailBySubject: map[string]string{"author-1": "author@example.com"},
		emailsByGroup:  map[string][]string{"approvers": {"a@example.com", "b@example.com"}},
	}
	mailer := &recordingMailer{}
	svc := NewNotifyService(newFakeNotifyStore(), clockAt{time.Unix(0, 0).UTC()}, "default").
		WithMail(mailer, dir, "https://console.example.com/")
	ctx := context.Background()

	// A recipient-addressed notification mails that one person, with the link
	// joined onto the console base.
	n := notify.Notification{Recipient: "author-1", Kind: notify.ChangeMerged,
		Title: "Merged", Body: "your change merged", Link: "/changes/x", Tenant: "default"}
	if err := svc.mailNotification(ctx, n); err != nil {
		t.Fatalf("mail recipient: %v", err)
	}
	if len(mailer.to) != 1 || mailer.to[0] != "author@example.com" {
		t.Fatalf("recipient mail went to %v", mailer.to)
	}
	if !strings.Contains(mailer.body, "https://console.example.com/changes/x") {
		t.Fatalf("link not appended: %q", mailer.body)
	}

	// An audience-addressed notification mails every seen member of the group.
	a := notify.Notification{Audience: "approvers", Kind: notify.ApprovalNeeded,
		Title: "Review", Tenant: "default"}
	if err := svc.mailNotification(ctx, a); err != nil {
		t.Fatalf("mail audience: %v", err)
	}
	if len(mailer.to) != 2 {
		t.Fatalf("audience mail went to %v", mailer.to)
	}
}

func TestNotifyNoMailerNoSend(t *testing.T) {
	// Without WithMail, emitting never tries to send.
	store := newFakeNotifyStore()
	svc := NewNotifyService(store, clockAt{time.Unix(0, 0).UTC()}, "default")
	if err := svc.Emit(context.Background(), notify.Notification{
		Recipient: "u", Kind: notify.GateFailed, Title: "x"}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if len(store.added) != 1 {
		t.Fatal("in-app notification should still be stored")
	}
}

func TestNotifyServiceReadFlow(t *testing.T) {
	store := newFakeNotifyStore()
	svc := NewNotifyService(store, clockAt{time.Unix(0, 0).UTC()}, "acme")
	ctx := context.Background()

	// One direct, one to a group the reader is in.
	_ = svc.Emit(ctx, notify.Notification{Recipient: "u", Kind: notify.GateFailed, Title: "a"})
	_ = svc.Emit(ctx, notify.Notification{Audience: "approvers", Kind: notify.ApprovalNeeded, Title: "b"})

	if n, _ := svc.Unread(ctx, "u", []string{"approvers"}); n != 2 {
		t.Fatalf("want 2 unread, got %d", n)
	}
	// A reader not in the audience only sees their direct one.
	if n, _ := svc.Unread(ctx, "u", nil); n != 1 {
		t.Fatalf("want 1 unread without membership, got %d", n)
	}

	if err := svc.MarkAllRead(ctx, "u", []string{"approvers"}); err != nil {
		t.Fatalf("mark all: %v", err)
	}
	if n, _ := svc.Unread(ctx, "u", []string{"approvers"}); n != 0 {
		t.Fatalf("want 0 unread after mark-all, got %d", n)
	}
}

// TestMailWorthyKeepsTheInboxUsable: measured on production 2026-08-05,
// approving one core update produced six e-mails. The generic progress
// notifications fire on a change submit AND again on its merge, on top of the
// change flow's own more specific messages. An operator who gets six mails for
// one click filters the folder, and then misses the one that mattered.
//
// Everything is still delivered in-app. This decides only what also leaves the
// console, and the test is whether it needs somebody who is NOT looking at it.
func TestMailWorthyKeepsTheInboxUsable(t *testing.T) {
	worthy := []notify.Kind{
		notify.ApprovalNeeded,     // a review is waiting on a person
		notify.ElevationRequested, // somebody is standing at a machine
		notify.GateFailed,         // a write was refused
		notify.WipeExecuted,       // a device destroyed its keys
	}
	for _, k := range worthy {
		if !mailWorthy(k) {
			t.Errorf("%s should reach an inbox", k)
		}
	}
	// Addressed to the person who just clicked, who is already looking at the
	// console - and superseded by their own outcome seconds later.
	for _, k := range []notify.Kind{notify.WritePending, notify.WriteApplied} {
		if mailWorthy(k) {
			t.Errorf("%s should stay in-app", k)
		}
	}
}

// TestEmitStoresEverythingEvenUnmailedKinds: the filter must narrow e-mail
// only. An in-app notification that stopped being recorded because it is not
// worth an e-mail would be a worse bug than the noise it fixes.
func TestEmitStoresEverythingEvenUnmailedKinds(t *testing.T) {
	store := newFakeNotifyStore()
	mailer := &recordingMailer{}
	svc := NewNotifyService(store, clockAt{time.Unix(0, 0).UTC()}, "default").
		WithMail(mailer, fakeUserDir{
			emailBySubject: map[string]string{"u": "u@example.com"},
		}, "https://console.example.com")

	if err := svc.Emit(context.Background(), notify.Notification{
		Recipient: "u", Kind: notify.WritePending, Title: "validating"}); err != nil {
		t.Fatal(err)
	}
	if len(store.added) != 1 {
		t.Fatal("an unmailed kind must still be recorded in-app")
	}
}
