package api

import (
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// TestMeReportsRoles: /me derives roles live and respects visibility.
func TestMeReportsRoles(t *testing.T) {
	// Scoped viewer: role at own group only, no org role reported.
	srv := newVisAPI(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})
	code, body := get(t, srv, "/api/v1/me")
	if code != 200 {
		t.Fatalf("me = %d", code)
	}
	page := string(body)
	for _, want := range []string{`"subject":"u"`, `"group:alpha":"viewer"`} {
		if !strings.Contains(page, want) {
			t.Errorf("me missing %q in %s", want, page)
		}
	}
	for _, leak := range []string{`"org":`, "beta"} {
		if strings.Contains(page, leak) {
			t.Errorf("me leaked %q", leak)
		}
	}
}

// TestAuditTrail: commit history is served to org viewers.
func TestAuditTrail(t *testing.T) {
	srv := newVisAPI(t, identity.User{Subject: "o", Groups: []string{"org-team"}})
	code, body := get(t, srv, "/api/v1/audit?limit=5")
	if code != 200 || !strings.Contains(string(body), `"subject":"seed"`) {
		t.Fatalf("audit = %d %s", code, body)
	}
	// Scoped viewers get 403: commits span every scope.
	scoped := newVisAPI(t, identity.User{Subject: "u", Groups: []string{"alpha-team"}})
	if code, _ := get(t, scoped, "/api/v1/audit"); code != 403 {
		t.Fatalf("scoped audit = %d, want 403", code)
	}
}
