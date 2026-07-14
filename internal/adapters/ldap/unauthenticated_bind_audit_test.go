package ldap

import "testing"

// TestNewRejectsBindDNWithEmptyPassword guards against a silent
// "unauthenticated bind" (RFC 4513 5.1.2): a bind DN configured with an
// empty password must refuse to start rather than let the directory accept
// it as an anonymous-but-successful bind.
func TestNewRejectsBindDNWithEmptyPassword(t *testing.T) {
	_, err := New(Config{URL: "ldaps://x", BaseDN: "dc=x", BindDN: "cn=svc,dc=x"})
	if err == nil {
		t.Fatal("BindDN with empty BindPassword accepted")
	}
}

// TestNewAllowsBindDNWithPassword is the control case: a real credential
// pair still constructs cleanly.
func TestNewAllowsBindDNWithPassword(t *testing.T) {
	if _, err := New(Config{URL: "ldaps://x", BaseDN: "dc=x", BindDN: "cn=svc,dc=x", BindPassword: "s3cret"}); err != nil {
		t.Fatalf("valid bind credentials rejected: %v", err)
	}
}

// TestNewAllowsAnonymousSearch is the other control case: no BindDN at all
// (anonymous search) must not be confused with the empty-password footgun.
func TestNewAllowsAnonymousSearch(t *testing.T) {
	if _, err := New(Config{URL: "ldaps://x", BaseDN: "dc=x"}); err != nil {
		t.Fatalf("anonymous (no BindDN) config rejected: %v", err)
	}
}
