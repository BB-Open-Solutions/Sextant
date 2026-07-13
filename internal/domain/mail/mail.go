// Package mail is the pure domain for outbound e-mail: a per-tenant SMTP
// configuration and a message. No I/O and no secret material lives here - the
// password is carried either as the NAME of a secret reference (the default,
// resolved out of band) or as an opaque already-encrypted blob (the opt-in
// console path). Building an actual transport and sending is the adapter's job.
package mail

import (
	"fmt"
	"strings"
)

// Security is how the client protects the SMTP connection.
type Security string

const (
	// StartTLS upgrades a plaintext connection with STARTTLS (the common 587
	// submission port). This is the default and what most providers want.
	StartTLS Security = "starttls"
	// TLS dials an implicit TLS connection (the 465 submission port).
	TLS Security = "tls"
	// None is an unencrypted connection - only ever acceptable to a loopback
	// relay, never across a network. Rejected unless the host is local.
	None Security = "none"
)

// Config is one tenant's SMTP settings. Exactly one credential source is set
// when a username is given: PasswordRef (a secret name resolved from agenix or
// a cluster Secret - the default) or PasswordEnc (a value entered in the
// console and encrypted at rest). Both empty means anonymous relay.
type Config struct {
	Host        string   `json:"host"`
	Port        int      `json:"port"`
	From        string   `json:"from"`
	Username    string   `json:"username,omitempty"`
	PasswordRef string   `json:"passwordRef,omitempty"`
	PasswordEnc []byte   `json:"-"` // never serialised to the client
	Security    Security `json:"security"`
}

// HasEnteredSecret reports whether this config carries an encrypted,
// console-entered password (path b) rather than a reference (path a).
func (c Config) HasEnteredSecret() bool { return len(c.PasswordEnc) > 0 }

// Validate rejects a configuration that could not send mail. It does not
// resolve or decrypt anything - only shape.
func (c Config) Validate() error {
	if strings.TrimSpace(c.Host) == "" {
		return fmt.Errorf("smtp: host is required")
	}
	if c.Port < 1 || c.Port > 65535 {
		return fmt.Errorf("smtp: port must be 1-65535 (got %d)", c.Port)
	}
	if !looksLikeEmail(c.From) {
		return fmt.Errorf("smtp: from must be an e-mail address")
	}
	switch c.Security {
	case StartTLS, TLS:
	case None:
		if !isLocalHost(c.Host) {
			return fmt.Errorf("smtp: unencrypted (none) is only allowed for a local relay")
		}
	default:
		return fmt.Errorf("smtp: unknown security %q", c.Security)
	}
	if c.Username != "" && c.PasswordRef != "" && c.HasEnteredSecret() {
		return fmt.Errorf("smtp: set either a password reference or an entered password, not both")
	}
	return nil
}

// Message is one e-mail to send. Recipients are addresses already resolved
// from notification audiences; the body is plain text.
type Message struct {
	To      []string
	Subject string
	Body    string
}

// Validate rejects an unsendable message.
func (m Message) Validate() error {
	if len(m.To) == 0 {
		return fmt.Errorf("mail: no recipients")
	}
	for _, a := range m.To {
		if !looksLikeEmail(a) {
			return fmt.Errorf("mail: %q is not an e-mail address", a)
		}
	}
	if strings.TrimSpace(m.Subject) == "" {
		return fmt.Errorf("mail: empty subject")
	}
	return nil
}

// looksLikeEmail is a deliberately loose check: exactly one @, non-empty local
// and domain parts, a dot in the domain. Real deliverability is the server's
// verdict, not ours.
func looksLikeEmail(s string) bool {
	s = strings.TrimSpace(s)
	at := strings.IndexByte(s, '@')
	if at <= 0 || at != strings.LastIndexByte(s, '@') || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}

// isLocalHost reports whether host is a loopback name, so an unencrypted relay
// is only ever permitted on the same machine.
func isLocalHost(host string) bool {
	switch strings.ToLower(strings.TrimSpace(host)) {
	case "localhost", "127.0.0.1", "::1":
		return true
	}
	return false
}
