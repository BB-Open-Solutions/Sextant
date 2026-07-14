package postgres

import (
	"context"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

// TestMailConfigRoundTrip covers Get/Put for the encrypted-password path
// (password_enc): ciphertext and every field must survive a round trip, and
// a second Put (upsert) must fully replace the prior config.
func TestMailConfigRoundTrip(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if _, ok, err := s.GetMailConfig(ctx, "t1"); err != nil || ok {
		t.Fatalf("empty get = %v %v", ok, err)
	}

	c := mail.Config{
		Host: "smtp.example.com", Port: 587, From: "sextant@example.com",
		Username: "svc", PasswordEnc: []byte("sealed-bytes-not-plaintext"),
		Security: mail.StartTLS,
	}
	if err := s.PutMailConfig(ctx, "t1", c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMailConfig(ctx, "t1")
	if err != nil || !ok {
		t.Fatalf("get = %v %v", ok, err)
	}
	if got.Host != c.Host || got.Port != c.Port || got.From != c.From ||
		got.Username != c.Username || got.Security != c.Security {
		t.Fatalf("fields = %+v, want %+v", got, c)
	}
	if string(got.PasswordEnc) != string(c.PasswordEnc) {
		t.Fatalf("sealed password = %q, want %q", got.PasswordEnc, c.PasswordEnc)
	}
	if got.PasswordRef != "" {
		t.Fatalf("password ref = %q, want empty on the encrypted path", got.PasswordRef)
	}

	// Upsert (ON CONFLICT) fully replaces the prior config.
	c2 := mail.Config{Host: "smtp2.example.com", Port: 465, From: "ops@example.com", Security: mail.TLS}
	if err := s.PutMailConfig(ctx, "t1", c2); err != nil {
		t.Fatal(err)
	}
	got2, _, _ := s.GetMailConfig(ctx, "t1")
	if got2.Host != c2.Host || got2.Port != c2.Port || len(got2.PasswordEnc) != 0 {
		t.Fatalf("after upsert = %+v, want replaced by %+v", got2, c2)
	}
}

// TestMailConfigReferenceOnlyPath round-trips password_ref distinctly from
// the encrypted path (path a: an external secret reference, never a sealed
// blob in this row).
func TestMailConfigReferenceOnlyPath(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	c := mail.Config{
		Host: "smtp.example.com", Port: 587, From: "sextant@example.com",
		PasswordRef: "vault:smtp/prod", Security: mail.StartTLS,
	}
	if err := s.PutMailConfig(ctx, "t2", c); err != nil {
		t.Fatal(err)
	}
	got, ok, err := s.GetMailConfig(ctx, "t2")
	if err != nil || !ok {
		t.Fatalf("get = %v %v", ok, err)
	}
	if got.PasswordRef != c.PasswordRef {
		t.Fatalf("password ref = %q, want %q", got.PasswordRef, c.PasswordRef)
	}
	if len(got.PasswordEnc) != 0 {
		t.Fatalf("password enc = %q, want empty on the reference-only path", got.PasswordEnc)
	}
}

// TestMailConfigTenantIsolation guards the tenant predicate: one tenant's
// SMTP config (including its sealed password) must never surface for
// another tenant's Get.
func TestMailConfigTenantIsolation(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.PutMailConfig(ctx, "org-a", mail.Config{
		Host: "a.example.com", Port: 587, From: "a@example.com",
		PasswordEnc: []byte("org-a-secret"), Security: mail.StartTLS,
	}); err != nil {
		t.Fatal(err)
	}

	if _, ok, err := s.GetMailConfig(ctx, "org-b"); err != nil || ok {
		t.Fatalf("org-b get = %v %v, want not found", ok, err)
	}

	a, ok, err := s.GetMailConfig(ctx, "org-a")
	if err != nil || !ok || a.Host != "a.example.com" {
		t.Fatalf("org-a get = %+v %v %v", a, ok, err)
	}
}

// TestMailConfigDelete removes the tenant's config.
func TestMailConfigDelete(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.PutMailConfig(ctx, "t3", mail.Config{Host: "x", Port: 587, From: "x@example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMailConfig(ctx, "t3"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetMailConfig(ctx, "t3"); err != nil || ok {
		t.Fatalf("after delete = %v %v, want not found", ok, err)
	}
}
