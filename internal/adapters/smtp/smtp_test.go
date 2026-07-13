package smtp

import (
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

func TestBuildRejectsHeaderInjection(t *testing.T) {
	// A subject carrying CRLF must not smuggle a second header into the message.
	msg := mail.Message{
		To:      []string{"a@b.com"},
		Subject: "hello\r\nBcc: victim@evil.com",
		Body:    "line1\nline2",
	}
	out := string(build("no-reply@example.com", msg))
	// The CRLF must be stripped so "Bcc:" cannot start its own header line; it
	// may still appear inline within the (now single-line) Subject.
	if strings.Contains(out, "\r\nBcc:") {
		t.Fatalf("header injection not stripped:\n%s", out)
	}
	if strings.Count(out, "Subject:") != 1 {
		t.Fatalf("subject split into multiple headers:\n%s", out)
	}
	// The body newline is normalised to CRLF.
	if !strings.Contains(out, "line1\r\nline2") {
		t.Fatal("body line endings not normalised to CRLF")
	}
}

func TestAddrOnly(t *testing.T) {
	cases := map[string]string{
		"no-reply@example.com":      "no-reply@example.com",
		"Sextant <no-reply@ex.com>": "no-reply@ex.com",
		"  spaced@ex.com  ":         "spaced@ex.com",
	}
	for in, want := range cases {
		if got := addrOnly(in); got != want {
			t.Errorf("addrOnly(%q) = %q, want %q", in, got, want)
		}
	}
}
