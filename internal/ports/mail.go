package ports

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

// Mailer sends one message over SMTP using a resolved configuration. The
// password is passed already resolved to plaintext (empty means anonymous
// relay); resolving a reference or decrypting an entered secret is the app
// service's job, so the adapter never touches secret storage.
type Mailer interface {
	Send(ctx context.Context, cfg mail.Config, password string, msg mail.Message) error
}

// MailConfigStore persists one SMTP configuration per tenant. The stored
// config may carry an encrypted password blob (PasswordEnc); the store keeps
// it opaque and never logs it.
type MailConfigStore interface {
	GetMailConfig(ctx context.Context, tenant string) (mail.Config, bool, error)
	PutMailConfig(ctx context.Context, tenant string, c mail.Config) error
	DeleteMailConfig(ctx context.Context, tenant string) error
}
