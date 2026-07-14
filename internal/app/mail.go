package app

import (
	"context"
	"fmt"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// SecretReader resolves a secret reference name to its value, reading from
// wherever the deployment mounts secrets (agenix, a cluster Secret). Injected
// so the service stays testable and free of filesystem knowledge.
type SecretReader func(name string) (string, error)

// MailService owns per-tenant SMTP: it stores the configuration, resolves the
// password (a mounted reference by default, or an at-rest encrypted value the
// operator entered), and sends. It is the only place a mail password becomes
// plaintext, and only for the duration of one send.
type MailService struct {
	store   ports.MailConfigStore
	mailer  ports.Mailer
	sealer  secretbox.Sealer
	readRef SecretReader
	tenant  string
}

// NewMailService wires the service. readRef may be nil when no secret mount is
// configured; the reference path then fails clearly at send time.
func NewMailService(store ports.MailConfigStore, mailer ports.Mailer, sealer secretbox.Sealer, readRef SecretReader, tenant string) *MailService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &MailService{store: store, mailer: mailer, sealer: sealer, readRef: readRef, tenant: tenant}
}

// CanStoreEnteredSecret reports whether the encryption key is present, so the
// console can offer (or hide) the "type a password" option.
func (s *MailService) CanStoreEnteredSecret() bool { return s.sealer.Enabled() }

// Config returns the tenant's stored SMTP config, if any. The encrypted
// password blob is cleared from the copy so it never leaves the service.
func (s *MailService) Config(ctx context.Context) (mail.Config, bool, error) {
	c, ok, err := s.store.GetMailConfig(ctx, s.tenant)
	if err != nil || !ok {
		return mail.Config{}, ok, err
	}
	c.PasswordEnc = nil
	return c, true, nil
}

// Save validates and stores the config. enteredPassword, when non-empty, is
// encrypted at rest and takes the entered-secret path (requiring a key);
// otherwise the config keeps its reference, or - when editing with a blank
// password and no reference - preserves the previously stored encrypted value.
func (s *MailService) Save(ctx context.Context, cfg mail.Config, enteredPassword string) error {
	switch {
	case enteredPassword != "":
		if !s.sealer.Enabled() {
			return fmt.Errorf("cannot store an entered password: no encryption key configured; use a secret reference instead")
		}
		sealed, err := s.sealer.Seal([]byte(enteredPassword))
		if err != nil {
			return err
		}
		cfg.PasswordEnc = sealed
		cfg.PasswordRef = ""
	case cfg.PasswordRef == "":
		// No new password and no reference: keep any previously stored secret so
		// an edit of the host/port does not silently drop the password. If the
		// prior-config read itself errors, fail closed rather than saving with
		// the password wiped - a transient read failure must not look like an
		// intentional password removal.
		prev, ok, err := s.store.GetMailConfig(ctx, s.tenant)
		if err != nil {
			return fmt.Errorf("reading the stored SMTP config: %w", err)
		}
		if ok {
			cfg.PasswordEnc = prev.PasswordEnc
		}
	}
	if err := cfg.Validate(); err != nil {
		return err
	}
	return s.store.PutMailConfig(ctx, s.tenant, cfg)
}

// Delete removes the tenant's SMTP configuration.
func (s *MailService) Delete(ctx context.Context) error {
	return s.store.DeleteMailConfig(ctx, s.tenant)
}

// Send delivers one message using the tenant's configuration. It errors when
// no SMTP is configured, so callers can treat "no mail" distinctly from "mail
// failed".
func (s *MailService) Send(ctx context.Context, msg mail.Message) error {
	cfg, ok, err := s.store.GetMailConfig(ctx, s.tenant)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("no SMTP configured for this organisation")
	}
	pw, err := s.resolve(cfg)
	if err != nil {
		return err
	}
	return s.mailer.Send(ctx, cfg, pw, msg)
}

// SendTo is the notifier's entry point: send a plain message to addresses the
// notifier already resolved. It is a thin wrapper over Send so the notifier
// never builds a mail.Message itself.
func (s *MailService) SendTo(ctx context.Context, to []string, subject, body string) error {
	return s.Send(ctx, mail.Message{To: to, Subject: subject, Body: body})
}

// SendTest sends a canned message proving the configuration works.
func (s *MailService) SendTest(ctx context.Context, to string) error {
	return s.Send(ctx, mail.Message{
		To:      []string{to},
		Subject: "Sextant test e-mail",
		Body:    "This is a test message from Sextant. If you received it, this organisation's SMTP settings work.",
	})
}

// resolve turns a stored config's credential into plaintext for one send: an
// encrypted value is decrypted, a reference is read from the secret mount, and
// no credential yields an empty string (anonymous relay).
func (s *MailService) resolve(cfg mail.Config) (string, error) {
	switch {
	case cfg.HasEnteredSecret():
		pw, err := s.sealer.Open(cfg.PasswordEnc)
		if err != nil {
			return "", fmt.Errorf("decrypting the SMTP password failed: %w", err)
		}
		return string(pw), nil
	case cfg.PasswordRef != "":
		if s.readRef == nil {
			return "", fmt.Errorf("SMTP references a secret %q but no secret mount is configured", cfg.PasswordRef)
		}
		pw, err := s.readRef(cfg.PasswordRef)
		if err != nil {
			return "", fmt.Errorf("reading SMTP secret %q: %w", cfg.PasswordRef, err)
		}
		return pw, nil
	default:
		return "", nil
	}
}
