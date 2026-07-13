package ports

import "context"

// UserDirectory is the address book the notifier uses to deliver by e-mail. It
// is populated from what the console already sees at login (subject, e-mail,
// name, groups), so no extra directory integration is needed to mail a
// notification. A user who has never logged in is simply not reachable by mail
// yet - in-app delivery still reaches them.
type UserDirectory interface {
	// RecordUser upserts what a login revealed about a user.
	RecordUser(ctx context.Context, tenant, subject, email, name string, groups []string) error
	// EmailForSubject returns a known user's e-mail, if any.
	EmailForSubject(ctx context.Context, tenant, subject string) (string, bool, error)
	// EmailsForAudience returns the e-mails of every seen user in a group.
	EmailsForAudience(ctx context.Context, tenant, group string) ([]string, error)
}
