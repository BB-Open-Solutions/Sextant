package smtp

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

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

// --- Send integration tests, against a hand-rolled in-process fake SMTP
// server. No external deps and no mocking of our own code: these exercise
// the real net/smtp client against a real (if minimal) server implementation
// over a real loopback TCP connection, including a real TLS handshake for
// the STARTTLS cases.

// envelope is what the fake server observed for one SMTP transaction.
type envelope struct {
	from     string
	rcpt     []string
	data     string
	authUser string
	authPass string
}

// fakeServerConfig controls how the fake server behaves for one test.
type fakeServerConfig struct {
	advertiseStartTLS bool        // advertise STARTTLS in EHLO and honour it
	directTLS         bool        // speak TLS from the first byte (implicit-TLS / port 465 style)
	tlsConf           *tls.Config // certificate to present when advertiseStartTLS or directTLS is set
	rejectRcpt        bool        // reply 550 to every RCPT TO
	rejectAuth        bool        // reply 535 to AUTH PLAIN instead of 235
	silent            bool        // accept the connection and never say a word (deadline test)
}

// startFakeSMTP starts a single-connection fake SMTP server on 127.0.0.1 and
// returns its address plus a channel that receives the envelope once a
// transaction completes. The server and any accepted connection are torn
// down via t.Cleanup.
func startFakeSMTP(t *testing.T, cfg fakeServerConfig) (host string, port int) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}

	var mu sync.Mutex
	var conns []net.Conn
	track := func(c net.Conn) {
		mu.Lock()
		conns = append(conns, c)
		mu.Unlock()
	}
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
	})

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return // listener closed by t.Cleanup
		}
		track(conn)

		if cfg.silent {
			return // deliberately say nothing; the client's deadline must fire
		}

		if cfg.directTLS {
			tconn := tls.Server(conn, cfg.tlsConf)
			track(tconn)
			if err := tconn.Handshake(); err != nil {
				return // expected: client refuses the self-signed cert
			}
			serveFakeSMTP(tconn, cfg)
			return
		}
		serveFakeSMTP(conn, cfg)
	}()

	h, p, err := net.SplitHostPort(ln.Addr().String())
	if err != nil {
		t.Fatalf("split listener addr: %v", err)
	}
	portNum, err := strconv.Atoi(p)
	if err != nil {
		t.Fatalf("parse port: %v", err)
	}
	return h, portNum
}

// serveFakeSMTP runs one SMTP conversation to completion (or until the peer
// goes away). It captures the transaction into env and, once DATA is
// complete, records env so the test can read it via getEnvelope.
func serveFakeSMTP(conn net.Conn, cfg fakeServerConfig) {
	defer func() { _ = conn.Close() }()
	br := bufio.NewReader(conn)
	send := func(format string, args ...any) {
		_, _ = fmt.Fprintf(conn, format+"\r\n", args...)
	}

	send("220 fake.smtp ESMTP ready")

	var env envelope
	inData := false

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case inData:
			if line == "." {
				inData = false
				send("250 OK: queued")
				lastEnvelope(env)
				continue
			}
			text := line
			if strings.HasPrefix(text, "..") {
				text = text[1:] // RFC 5321 transparency: undo the sender's dot-stuffing
			}
			env.data += text + "\n"

		case strings.HasPrefix(upper, "EHLO"):
			send("250-fake.smtp Hello")
			if cfg.advertiseStartTLS {
				send("250-STARTTLS")
			}
			send("250-AUTH PLAIN")
			send("250 8BITMIME")

		case strings.HasPrefix(upper, "STARTTLS"):
			send("220 Go ahead")
			tconn := tls.Server(conn, cfg.tlsConf)
			if err := tconn.Handshake(); err != nil {
				return // expected: client refuses the self-signed cert
			}
			conn = tconn
			br = bufio.NewReader(conn)
			send = func(format string, args ...any) {
				_, _ = fmt.Fprintf(conn, format+"\r\n", args...)
			}

		case strings.HasPrefix(upper, "AUTH PLAIN"):
			if cfg.rejectAuth {
				send("535 5.7.8 authentication failed")
				continue
			}
			fields := strings.Fields(line)
			if len(fields) == 3 {
				if raw, err := base64.StdEncoding.DecodeString(fields[2]); err == nil {
					parts := strings.SplitN(string(raw), "\x00", 3)
					if len(parts) == 3 {
						env.authUser = parts[1]
						env.authPass = parts[2]
					}
				}
			}
			send("235 2.7.0 Authentication successful")

		case strings.HasPrefix(upper, "MAIL FROM"):
			env.from = extractAddr(line)
			send("250 OK")

		case strings.HasPrefix(upper, "RCPT TO"):
			if cfg.rejectRcpt {
				send("550 5.1.1 no such user")
				continue
			}
			env.rcpt = append(env.rcpt, extractAddr(line))
			send("250 OK")

		case strings.HasPrefix(upper, "DATA"):
			send("354 Start mail input; end with <CRLF>.<CRLF>")
			inData = true

		case strings.HasPrefix(upper, "QUIT"):
			send("221 Bye")
			return

		default:
			send("500 unrecognized command")
		}
	}
}

// The fake server runs in its own goroutine; envelopeCh hands the completed
// transaction back to whichever test is currently running. Tests run
// sequentially (no t.Parallel here) so a single package-level channel,
// drained by each test before it returns, is enough.
var envelopeCh = make(chan envelope, 1)

func lastEnvelope(env envelope) {
	select {
	case envelopeCh <- env:
	default:
	}
}

// extractAddr pulls the bare address out of a `MAIL FROM:<addr> ...` or
// `RCPT TO:<addr>` command line.
func extractAddr(line string) string {
	i := strings.IndexByte(line, '<')
	j := strings.IndexByte(line, '>')
	if i >= 0 && j > i {
		return line[i+1 : j]
	}
	return ""
}

// selfSignedCert generates a throwaway self-signed certificate covering the
// given hosts (DNS names or IPs), for the STARTTLS/TLS fake-server tests.
func selfSignedCert(t *testing.T, hosts ...string) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: hosts[0]},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			tmpl.IPAddresses = append(tmpl.IPAddresses, ip)
		} else {
			tmpl.DNSNames = append(tmpl.DNSNames, h)
		}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

func validMessage() mail.Message {
	return mail.Message{
		To:      []string{"rcpt@example.com"},
		Subject: "hi there",
		Body:    "hello world",
	}
}

// TestSendPlainHappyPath drives the full happy path with no transport
// security (loopback-only, as mail.Config.Validate requires for
// mail.None) and PLAIN auth, and checks the server actually received the
// envelope and credentials the adapter was told to send.
func TestSendPlainHappyPath(t *testing.T) {
	host, port := startFakeSMTP(t, fakeServerConfig{})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "Sender <sender@example.com>",
		Username: "svc-mailer",
		Security: mail.None,
	}
	m := New(2 * time.Second)
	if err := m.Send(context.Background(), cfg, "s3cret", validMessage()); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case env := <-envelopeCh:
		if env.from != "sender@example.com" {
			t.Errorf("from = %q, want sender@example.com", env.from)
		}
		if len(env.rcpt) != 1 || env.rcpt[0] != "rcpt@example.com" {
			t.Errorf("rcpt = %v, want [rcpt@example.com]", env.rcpt)
		}
		if env.authUser != "svc-mailer" || env.authPass != "s3cret" {
			t.Errorf("auth = %q/%q, want svc-mailer/s3cret", env.authUser, env.authPass)
		}
		if !strings.Contains(env.data, "Subject: hi there") {
			t.Errorf("data missing subject header:\n%s", env.data)
		}
		if !strings.Contains(env.data, "hello world") {
			t.Errorf("data missing body:\n%s", env.data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server never observed a completed transaction")
	}
}

// TestSendStartTLSUntrustedCert documents what the adapter actually does: it
// dials plaintext, negotiates STARTTLS, and verifies the peer certificate
// with tls.Config{ServerName: cfg.Host} - no InsecureSkipVerify and no
// custom RootCAs. Against a self-signed cert (not in any trust store) that
// verification must fail cleanly, with the client aborting the handshake
// rather than hanging.
func TestSendStartTLSUntrustedCert(t *testing.T) {
	cert := selfSignedCert(t, "127.0.0.1")
	host, port := startFakeSMTP(t, fakeServerConfig{
		advertiseStartTLS: true,
		tlsConf:           &tls.Config{Certificates: []tls.Certificate{cert}},
	})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "sender@example.com",
		Security: mail.StartTLS,
	}
	m := New(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := m.Send(ctx, cfg, "", validMessage())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send succeeded against a self-signed cert; adapter should verify certificates")
	}
	if !strings.Contains(err.Error(), "starttls") {
		t.Errorf("error = %q, want it to mention starttls", err.Error())
	}
	var certErr *tls.CertificateVerificationError
	var unknownAuth x509.UnknownAuthorityError
	if !errors.As(err, &certErr) && !errors.As(err, &unknownAuth) {
		t.Errorf("error = %q, want a certificate-verification error", err.Error())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Send took %v against an untrusted cert; should fail fast, not hang", elapsed)
	}
}

// TestSendDirectTLSUntrustedCert is the same certificate-verification check
// as TestSendStartTLSUntrustedCert, but for mail.TLS (implicit TLS, the
// dial() branch that never goes through STARTTLS at all).
func TestSendDirectTLSUntrustedCert(t *testing.T) {
	cert := selfSignedCert(t, "127.0.0.1")
	host, port := startFakeSMTP(t, fakeServerConfig{
		directTLS: true,
		tlsConf:   &tls.Config{Certificates: []tls.Certificate{cert}},
	})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "sender@example.com",
		Security: mail.TLS,
	}
	m := New(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	start := time.Now()
	err := m.Send(ctx, cfg, "", validMessage())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send succeeded against a self-signed cert; adapter should verify certificates")
	}
	if !strings.Contains(err.Error(), "smtp tls dial") {
		t.Errorf("error = %q, want it to mention the tls dial", err.Error())
	}
	if elapsed > 5*time.Second {
		t.Fatalf("Send took %v against an untrusted cert; should fail fast, not hang", elapsed)
	}
}

// TestSendRcptRejected checks that a server-side refusal of a recipient
// surfaces as a clear, specific error rather than being swallowed or
// reported as a generic failure.
func TestSendRcptRejected(t *testing.T) {
	host, port := startFakeSMTP(t, fakeServerConfig{rejectRcpt: true})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "sender@example.com",
		Security: mail.None,
	}
	m := New(2 * time.Second)
	err := m.Send(context.Background(), cfg, "", validMessage())
	if err == nil {
		t.Fatal("Send succeeded despite the server rejecting RCPT TO")
	}
	if !strings.Contains(err.Error(), "smtp rcpt") {
		t.Errorf("error = %q, want it to mention smtp rcpt", err.Error())
	}
	if !strings.Contains(err.Error(), "rcpt@example.com") {
		t.Errorf("error = %q, want it to name the rejected recipient", err.Error())
	}
}

// TestSendDeadlineExceeded checks that a server which accepts the TCP
// connection and then never speaks (no greeting, nothing) makes Send fail
// once the context deadline passes, instead of hanging forever.
func TestSendDeadlineExceeded(t *testing.T) {
	host, port := startFakeSMTP(t, fakeServerConfig{silent: true})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "sender@example.com",
		Security: mail.None,
	}
	m := New(2 * time.Second)

	const deadline = 200 * time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), deadline)
	defer cancel()

	start := time.Now()
	err := m.Send(ctx, cfg, "", validMessage())
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Send succeeded against a server that never responded")
	}
	// Generous margin: the deadline must be honoured, not the dial timeout
	// (2s) or an unbounded hang. 10x the deadline is still far short of
	// dialTimeout, so this only fails if the deadline plumbing is broken.
	if elapsed > 10*deadline {
		t.Fatalf("Send took %v after a %v deadline; looks unbounded", elapsed, deadline)
	}
}

// TestSendAuthRejected checks that a server-side AUTH failure is reported as
// a specific, wrapped error instead of a generic one.
func TestSendAuthRejected(t *testing.T) {
	host, port := startFakeSMTP(t, fakeServerConfig{rejectAuth: true})

	cfg := mail.Config{
		Host:     host,
		Port:     port,
		From:     "sender@example.com",
		Username: "svc-mailer",
		Security: mail.None,
	}
	m := New(2 * time.Second)
	err := m.Send(context.Background(), cfg, "wrong", validMessage())
	if err == nil {
		t.Fatal("Send succeeded despite the server rejecting AUTH")
	}
	if !strings.Contains(err.Error(), "smtp auth") {
		t.Errorf("error = %q, want it to mention smtp auth", err.Error())
	}
}

// TestSendRejectsInvalidConfig checks that Send delegates to
// mail.Config.Validate up front and never touches the network for a
// config that could not possibly send mail.
func TestSendRejectsInvalidConfig(t *testing.T) {
	cfg := mail.Config{ /* no Host: fails Validate */ From: "sender@example.com", Security: mail.None}
	m := New(2 * time.Second)
	err := m.Send(context.Background(), cfg, "", validMessage())
	if err == nil {
		t.Fatal("Send succeeded with an invalid config (missing host)")
	}
}

// TestSendRejectsInvalidMessage checks that Send delegates to
// mail.Message.Validate up front.
func TestSendRejectsInvalidMessage(t *testing.T) {
	cfg := mail.Config{Host: "127.0.0.1", Port: 25, From: "sender@example.com", Security: mail.None}
	m := New(2 * time.Second)
	err := m.Send(context.Background(), cfg, "", mail.Message{ /* no recipients: fails Validate */ Subject: "hi"})
	if err == nil {
		t.Fatal("Send succeeded with an invalid message (no recipients)")
	}
}

// TestNewDefaultsNonPositiveTimeout checks the documented fallback: a
// non-positive dial timeout becomes 10s, a positive one passes through.
func TestNewDefaultsNonPositiveTimeout(t *testing.T) {
	for _, d := range []time.Duration{0, -1 * time.Second} {
		if m := New(d); m.dialTimeout != 10*time.Second {
			t.Errorf("New(%v).dialTimeout = %v, want the 10s default", d, m.dialTimeout)
		}
	}
	if m := New(3 * time.Second); m.dialTimeout != 3*time.Second {
		t.Errorf("New(3s).dialTimeout = %v, want 3s", m.dialTimeout)
	}
}
