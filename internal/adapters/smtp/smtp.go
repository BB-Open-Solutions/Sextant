// Package smtp is the outbound-mail adapter: it turns a resolved mail.Config
// plus a plaintext password into an actual SMTP submission. It implements
// ports.Mailer. Secret resolution (references, decryption) happens above this
// layer; here the password already is what it is.
package smtp

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/mail"
)

// Mailer sends mail over SMTP. The zero value is ready to use; dialTimeout
// bounds connect + handshake so a dead relay never wedges the caller.
type Mailer struct {
	dialTimeout time.Duration
}

// New builds a Mailer. A non-positive timeout falls back to 10s.
func New(dialTimeout time.Duration) *Mailer {
	if dialTimeout <= 0 {
		dialTimeout = 10 * time.Second
	}
	return &Mailer{dialTimeout: dialTimeout}
}

// Send submits one message. It honours the config's security mode, uses PLAIN
// auth when a username is set, and applies the context deadline to the whole
// exchange.
func (m *Mailer) Send(ctx context.Context, cfg mail.Config, password string, msg mail.Message) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	if err := msg.Validate(); err != nil {
		return err
	}
	addr := net.JoinHostPort(cfg.Host, fmt.Sprint(cfg.Port))

	c, err := m.dial(ctx, cfg, addr)
	if err != nil {
		return err
	}
	defer c.Close()

	if cfg.Username != "" {
		auth := smtp.PlainAuth("", cfg.Username, password, cfg.Host)
		if err := c.Auth(auth); err != nil {
			return fmt.Errorf("smtp auth: %w", err)
		}
	}
	if err := c.Mail(addrOnly(cfg.From)); err != nil {
		return fmt.Errorf("smtp from: %w", err)
	}
	for _, rcpt := range msg.To {
		if err := c.Rcpt(addrOnly(rcpt)); err != nil {
			return fmt.Errorf("smtp rcpt %q: %w", rcpt, err)
		}
	}
	wc, err := c.Data()
	if err != nil {
		return fmt.Errorf("smtp data: %w", err)
	}
	if _, err := wc.Write(build(cfg.From, msg)); err != nil {
		_ = wc.Close()
		return fmt.Errorf("smtp write: %w", err)
	}
	if err := wc.Close(); err != nil {
		return fmt.Errorf("smtp body close: %w", err)
	}
	return c.Quit()
}

// dial opens an SMTP client honouring the security mode, and wires the context
// deadline into the underlying connection.
func (m *Mailer) dial(ctx context.Context, cfg mail.Config, addr string) (*smtp.Client, error) {
	d := net.Dialer{Timeout: m.dialTimeout}
	if cfg.Security == mail.TLS {
		conn, err := tls.DialWithDialer(&d, "tcp", addr, &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12})
		if err != nil {
			return nil, fmt.Errorf("smtp tls dial: %w", err)
		}
		applyDeadline(ctx, conn)
		return smtp.NewClient(conn, cfg.Host)
	}
	conn, err := d.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("smtp dial: %w", err)
	}
	applyDeadline(ctx, conn)
	c, err := smtp.NewClient(conn, cfg.Host)
	if err != nil {
		return nil, err
	}
	if cfg.Security == mail.StartTLS {
		if err := c.StartTLS(&tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}); err != nil {
			_ = c.Close()
			return nil, fmt.Errorf("smtp starttls: %w", err)
		}
	}
	return c, nil
}

// applyDeadline pushes the context deadline onto the raw connection so a stall
// mid-exchange is bounded, not just the dial.
func applyDeadline(ctx context.Context, conn net.Conn) {
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetDeadline(dl)
	}
}

// build renders RFC 5322 headers and a plain-text body. Subject and body are
// server-controlled console strings, not attacker input, but header-injection
// is still refused by stripping CR/LF from the single-line headers.
func build(from string, msg mail.Message) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", oneLine(from))
	fmt.Fprintf(&b, "To: %s\r\n", oneLine(strings.Join(msg.To, ", ")))
	fmt.Fprintf(&b, "Subject: %s\r\n", oneLine(msg.Subject))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(msg.Body, "\n", "\r\n"))
	return []byte(b.String())
}

// oneLine strips CR and LF so a header value cannot inject extra headers.
func oneLine(s string) string {
	return strings.NewReplacer("\r", "", "\n", "").Replace(s)
}

// addrOnly extracts the bare address from a "Name <addr>" form for the SMTP
// envelope; a plain address passes through.
func addrOnly(s string) string {
	if i := strings.LastIndexByte(s, '<'); i >= 0 {
		if j := strings.IndexByte(s[i:], '>'); j > 0 {
			return strings.TrimSpace(s[i+1 : i+j])
		}
	}
	return strings.TrimSpace(s)
}
