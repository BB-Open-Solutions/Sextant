package app

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
)

type memMailStore struct {
	cfg *mail.Config
}

func (m *memMailStore) GetMailConfig(context.Context, string) (mail.Config, bool, error) {
	if m.cfg == nil {
		return mail.Config{}, false, nil
	}
	return *m.cfg, true, nil
}
func (m *memMailStore) PutMailConfig(_ context.Context, _ string, c mail.Config) error {
	m.cfg = &c
	return nil
}
func (m *memMailStore) DeleteMailConfig(context.Context, string) error { m.cfg = nil; return nil }

type capturingMailer struct {
	cfg      mail.Config
	password string
	msg      mail.Message
	sent     bool
}

func (c *capturingMailer) Send(_ context.Context, cfg mail.Config, pw string, msg mail.Message) error {
	c.cfg, c.password, c.msg, c.sent = cfg, pw, msg, true
	return nil
}

func testSealer(t *testing.T) secretbox.Sealer {
	t.Helper()
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i + 1)
	}
	s, err := secretbox.New(base64.StdEncoding.EncodeToString(key))
	if err != nil {
		t.Fatalf("sealer: %v", err)
	}
	return s
}

func baseCfg() mail.Config {
	return mail.Config{Host: "smtp.example.com", Port: 587, From: "no-reply@example.com",
		Username: "postbus", Security: mail.StartTLS}
}

func TestMailEnteredPasswordSealedAndResolved(t *testing.T) {
	store := &memMailStore{}
	mailer := &capturingMailer{}
	svc := NewMailService(store, mailer, testSealer(t), nil, "default")
	ctx := context.Background()

	if err := svc.Save(ctx, baseCfg(), "s3cr3t"); err != nil {
		t.Fatalf("save: %v", err)
	}
	// Stored encrypted, never plaintext.
	if store.cfg == nil || len(store.cfg.PasswordEnc) == 0 {
		t.Fatal("entered password was not sealed")
	}
	if string(store.cfg.PasswordEnc) == "s3cr3t" {
		t.Fatal("password stored in plaintext")
	}
	// Config() never leaks the blob.
	if c, _, _ := svc.Config(ctx); len(c.PasswordEnc) != 0 {
		t.Fatal("Config leaked the encrypted blob")
	}
	// Sending decrypts it back to plaintext for the mailer.
	if err := svc.SendTest(ctx, "ops@example.com"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if !mailer.sent || mailer.password != "s3cr3t" {
		t.Fatalf("mailer got password %q", mailer.password)
	}
}

func TestMailReferenceResolvedViaReader(t *testing.T) {
	store := &memMailStore{}
	mailer := &capturingMailer{}
	read := func(name string) (string, error) {
		if name == "smtp-pw" {
			return "from-mount", nil
		}
		return "", context.Canceled
	}
	svc := NewMailService(store, mailer, secretbox.Sealer{}, read, "default")
	ctx := context.Background()

	cfg := baseCfg()
	cfg.PasswordRef = "smtp-pw"
	if err := svc.Save(ctx, cfg, ""); err != nil {
		t.Fatalf("save: %v", err)
	}
	if err := svc.SendTest(ctx, "ops@example.com"); err != nil {
		t.Fatalf("send: %v", err)
	}
	if mailer.password != "from-mount" {
		t.Fatalf("reference not resolved: %q", mailer.password)
	}
}

func TestMailEnteredPasswordNeedsKey(t *testing.T) {
	// No key: the entered-secret path is refused (fail-closed).
	svc := NewMailService(&memMailStore{}, &capturingMailer{}, secretbox.Sealer{}, nil, "default")
	if svc.CanStoreEnteredSecret() {
		t.Fatal("should not offer entered secrets without a key")
	}
	if err := svc.Save(context.Background(), baseCfg(), "typed"); err == nil {
		t.Fatal("entered password without a key must be rejected")
	}
}

// erroringMailStore wraps a memMailStore and fails the next GetMailConfig
// call once, to test the prior-config read error path.
type erroringMailStore struct {
	memMailStore
	failNextGet bool
}

func (m *erroringMailStore) GetMailConfig(ctx context.Context, tenant string) (mail.Config, bool, error) {
	if m.failNextGet {
		m.failNextGet = false
		return mail.Config{}, false, errors.New("store unavailable")
	}
	return m.memMailStore.GetMailConfig(ctx, tenant)
}

// TestMailSaveFailsClosedOnPriorConfigReadError (finding: Save() silently
// dropped the stored SMTP password when the prior-config read errors): a
// transient read failure on the blank-password/no-reference edit path must
// surface as an error, not silently persist the config with the password
// wiped.
func TestMailSaveFailsClosedOnPriorConfigReadError(t *testing.T) {
	store := &erroringMailStore{}
	svc := NewMailService(store, &capturingMailer{}, testSealer(t), nil, "default")
	ctx := context.Background()

	if err := svc.Save(ctx, baseCfg(), "orig"); err != nil {
		t.Fatalf("seed save: %v", err)
	}
	sealed := append([]byte(nil), store.cfg.PasswordEnc...)

	// Edit with a blank password and no reference, but the prior-config read
	// errors: Save must fail, and must NOT have overwritten the stored config.
	store.failNextGet = true
	edit := baseCfg()
	edit.Host = "smtp2.example.com"
	if err := svc.Save(ctx, edit, ""); err == nil {
		t.Fatal("want the prior-config read error surfaced, not swallowed")
	}
	if string(store.cfg.PasswordEnc) != string(sealed) {
		t.Fatal("a failed save must not drop the previously stored password")
	}
	if store.cfg.Host == "smtp2.example.com" {
		t.Fatal("a failed save must not apply the edit either")
	}
}

func TestMailEditKeepsExistingSecret(t *testing.T) {
	store := &memMailStore{}
	svc := NewMailService(store, &capturingMailer{}, testSealer(t), nil, "default")
	ctx := context.Background()

	_ = svc.Save(ctx, baseCfg(), "orig")
	sealed := append([]byte(nil), store.cfg.PasswordEnc...)

	// Edit host with a blank password and no reference: keep the sealed value.
	edit := baseCfg()
	edit.Host = "smtp2.example.com"
	if err := svc.Save(ctx, edit, ""); err != nil {
		t.Fatalf("edit: %v", err)
	}
	if string(store.cfg.PasswordEnc) != string(sealed) {
		t.Fatal("editing dropped the stored password")
	}
	if store.cfg.Host != "smtp2.example.com" {
		t.Fatal("edit did not apply")
	}
}
