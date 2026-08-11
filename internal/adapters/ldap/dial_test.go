package ldap

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"math/big"
	"net"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// dial had no test, which for the two properties below is the wrong place to
// be relying on reading. Both are enforced by things that are easy to delete
// by accident and produce no error when deleted: certificate verification
// happens by default in crypto/tls, so a single added field turns it off, and
// the dial deadline is one appended option.
//
// Neither needs a directory. A TLS listener holding a self-signed certificate
// is enough, and it is the same shape as the production failure this guards:
// an ldaps:// endpoint whose certificate we do not trust.

// selfSignedTLSListener starts a TLS listener on loopback with a certificate
// signed by nobody, and returns its host:port. Nothing ever trusts it, which
// is the point.
func selfSignedTLSListener(t *testing.T) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:     []string{"localhost"},
	}
	der, err := x509.CreateCertificate(rand.Reader, &tmpl, &tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		MinVersion:   tls.VersionTLS12,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			// Complete the handshake so the client's verdict is about the
			// certificate rather than about a connection that went away.
			go func() {
				defer func() { _ = conn.Close() }()
				_ = conn.(*tls.Conn).Handshake()
			}()
		}
	}()
	return ln.Addr().String()
}

// The H3 property, from the other side. On 2026-08-11 the fleet was hardened
// so a device refuses a directory certificate it cannot verify
// (ldap_tls_reqcert=demand). The console reaches the same directory over the
// same TLS and must refuse it for the same reason.
//
// The guard is an absence: dial builds a tls.Config with only MinVersion, and
// crypto/tls verifies by default. Adding InsecureSkipVerify makes this pass
// silently, forever, against any certificate at all.
func TestLdapsRefusesACertificateItCannotVerify(t *testing.T) {
	addr := selfSignedTLSListener(t)
	d, err := New(Config{URL: "ldaps://" + addr, BaseDN: "dc=example,dc=org"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := d.dial(ctx)
	if err == nil {
		_ = conn.Close()
		t.Fatal("an ldaps:// endpoint with an untrusted certificate was accepted; " +
			"certificate verification is off")
	}
	// Name the reason rather than accept any failure: a refused connection or
	// a timeout would also be non-nil, and neither proves verification ran.
	var certErr x509.UnknownAuthorityError
	var hostErr x509.HostnameError
	if !errors.As(err, &certErr) && !errors.As(err, &hostErr) &&
		!strings.Contains(err.Error(), "certificate") {
		t.Fatalf("dial failed for the wrong reason: %v", err)
	}
}

// ListGroups turns an unreachable directory into ErrUnavailable rather than
// an opaque error, which is what lets the console degrade to "no groups"
// instead of failing the page. Driven through the same untrusted endpoint, so
// the error travels the real path.
func TestListGroupsReportsAnUnreachableDirectoryAsUnavailable(t *testing.T) {
	addr := selfSignedTLSListener(t)
	d, err := New(Config{URL: "ldaps://" + addr, BaseDN: "dc=example,dc=org"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	groups, err := d.ListGroups(ctx, "")
	if err == nil {
		t.Fatal("ListGroups succeeded against an endpoint it cannot verify")
	}
	if !errors.Is(err, ports.ErrUnavailable) {
		t.Errorf("error does not carry ErrUnavailable, so the caller cannot "+
			"tell 'directory down' from 'query wrong': %v", err)
	}
	if groups != nil {
		t.Errorf("groups = %v on a failed dial; a partial answer here would be "+
			"read as 'this directory has no groups'", groups)
	}
}

// The connect budget, which is where the real decision lives. A directory
// that hangs must not hang the page: without a bound, DialURL inherits the OS
// connect timeout of about a minute.
//
// Tested here rather than through a socket because through a socket it could
// not fail. See dialTimeout's own comment for the measurement.
func TestDialTimeoutTakesTheTighterOfCeilingAndDeadline(t *testing.T) {
	const ceiling = maxDial

	t.Run("no deadline uses the ceiling", func(t *testing.T) {
		if got := dialTimeout(context.Background()); got != ceiling {
			t.Errorf("got %v, want %v", got, ceiling)
		}
	})

	t.Run("a tighter deadline wins", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
		defer cancel()
		got := dialTimeout(ctx)
		if got >= ceiling || got <= 0 {
			t.Errorf("got %v; a 500ms deadline must produce something under the %v ceiling", got, ceiling)
		}
	})

	t.Run("a looser deadline does not raise the ceiling", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Hour)
		defer cancel()
		if got := dialTimeout(ctx); got != ceiling {
			t.Errorf("got %v, want the ceiling %v: a patient caller does not "+
				"buy the right to hang the page", got, ceiling)
		}
	})

	// The one that bites. net.Dialer reads a non-positive Timeout as NO
	// timeout, so handing it the remainder of an expired context would remove
	// the bound at exactly the moment it matters most.
	t.Run("an expired deadline keeps the ceiling rather than disabling it", func(t *testing.T) {
		ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(-time.Minute))
		defer cancel()
		got := dialTimeout(ctx)
		if got <= 0 {
			t.Fatalf("got %v; net.Dialer reads that as no timeout at all", got)
		}
		if got != ceiling {
			t.Errorf("got %v, want %v", got, ceiling)
		}
	})
}
