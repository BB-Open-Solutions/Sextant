package web

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// The device credential is shown to an operator exactly once, and it travels
// from the issuing POST to the device page in a cookie. That cookie is
// carrying a secret through the browser, so its attributes are the security
// control, and every one of them fails silently when it goes missing: the
// page still renders, the operator still sees the secret, and nothing
// anywhere reports that it also went somewhere it should not have.
//
// So each attribute is pinned by name with the reason attached, rather than
// left to whoever next edits this struct to notice what the fields were for.

func issuedCookie(t *testing.T, tag, secret string) *http.Cookie {
	t.Helper()
	rec := httptest.NewRecorder()
	setDevCredCookie(rec, tag, secret)
	for _, c := range rec.Result().Cookies() {
		if c.Name == devCredCookie {
			return c
		}
	}
	t.Fatalf("no %s cookie was set", devCredCookie)
	return nil
}

func TestDeviceCredentialCookieCarriesItsSecretSafely(t *testing.T) {
	c := issuedCookie(t, "lt-1", "s3cr3t-value")

	if c.Value != "s3cr3t-value" {
		t.Errorf("value = %q", c.Value)
	}

	// Hardcoded rather than derived from the request scheme. On a plain-HTTP
	// host the browser drops the cookie and the secret is never shown, which
	// is the right failure: the operator re-issues, and the secret has not
	// crossed the network in clear text.
	if !c.Secure {
		t.Error("Secure is off; the secret would travel in clear text on a " +
			"plain-HTTP host instead of simply not arriving")
	}

	// The page renders the value server-side. Script has no reason to read it,
	// and an XSS anywhere in the console would otherwise lift it.
	if !c.HttpOnly {
		t.Error("HttpOnly is off; any script in the console can read a device secret")
	}

	if c.SameSite != http.SameSiteStrictMode {
		t.Errorf("SameSite = %v, want Strict: a cross-site navigation must not "+
			"carry this cookie", c.SameSite)
	}

	// Scoped to the one device's page. Without the tag in the path the browser
	// would attach a device's secret to every other device's page too.
	if c.Path != "/devices/lt-1" {
		t.Errorf("Path = %q, want /devices/lt-1; a broader path sends one "+
			"device's secret to another device's page", c.Path)
	}

	// Short enough that a cookie left behind by a closed tab expires before
	// anybody comes back to the machine.
	if c.MaxAge <= 0 || c.MaxAge > 300 {
		t.Errorf("MaxAge = %d, want a small positive number of seconds", c.MaxAge)
	}
}

// The path is built from the tag, so two devices must not share a scope.
func TestDeviceCredentialCookieIsScopedPerDevice(t *testing.T) {
	one := issuedCookie(t, "lt-1", "a")
	two := issuedCookie(t, "lt-2", "b")
	if one.Path == two.Path {
		t.Fatalf("both devices got path %q; the scope does not distinguish them", one.Path)
	}
	if one.Path != "/devices/lt-1" || two.Path != "/devices/lt-2" {
		t.Errorf("paths = %q and %q", one.Path, two.Path)
	}
}

// The wire name, asserted here so the external round-trip test can use the
// literal a browser sees without importing an unexported constant.
func TestDeviceCredentialCookieName(t *testing.T) {
	if devCredCookie != "sextant_devcred" {
		t.Errorf("cookie name = %q; devcred_roundtrip_test.go hardcodes the old one", devCredCookie)
	}
}
