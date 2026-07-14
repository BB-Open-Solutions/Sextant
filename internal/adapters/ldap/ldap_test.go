package ldap

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestNewValidatesAndDefaults(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("empty config accepted")
	}
	d, err := New(Config{URL: "ldaps://x", BaseDN: "dc=x"})
	if err != nil {
		t.Fatal(err)
	}
	if d.cfg.GroupFilter != "(objectClass=groupOfNames)" || d.cfg.NameAttr != "cn" {
		t.Fatalf("defaults = %+v", d.cfg)
	}
}

func TestSearchFilterEscapesQuery(t *testing.T) {
	base := "(objectClass=groupOfNames)"
	if got := searchFilter(base, "cn", ""); got != base {
		t.Fatalf("empty query filter = %s", got)
	}
	got := searchFilter(base, "cn", "admins")
	if got != "(&(objectClass=groupOfNames)(cn=*admins*))" {
		t.Fatalf("filter = %s", got)
	}
	// Filter injection: metacharacters must arrive escaped, never raw.
	got = searchFilter(base, "cn", `*)(objectClass=*`)
	for _, raw := range []string{"(cn=**)", "objectClass=*))"} {
		if contains(got, raw) {
			t.Fatalf("unescaped metacharacters in %s", got)
		}
	}
	if !contains(got, `\2a`) || !contains(got, `\28`) {
		t.Fatalf("expected escaped * and ( in %s", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestBindIsCleartextOnlyWhenBindHappensOverPlainLDAP(t *testing.T) {
	cases := []struct {
		name                      string
		url, bindDN, bindPassword string
		want                      bool
	}{
		{"ldaps with password", "ldaps://x", "cn=svc", "secret", false},
		{"ldap with password", "ldap://x", "cn=svc", "secret", true},
		{"ldap no bind dn (anonymous, no credentials sent)", "ldap://x", "", "secret", false},
		{"ldap empty password (unauthenticated bind, no secret sent)", "ldap://x", "cn=svc", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bindIsCleartext(tc.url, tc.bindDN, tc.bindPassword); got != tc.want {
				t.Errorf("bindIsCleartext(%q,%q,%q) = %v, want %v",
					tc.url, tc.bindDN, tc.bindPassword, got, tc.want)
			}
		})
	}
}

// TestWarnCleartextBindOnceFiresOnce proves the cleartext-bind exposure is
// never silent (a WARN is logged) but also never spammed (once per
// Directory, guarded by sync.Once), for a plain ldap:// URL with a
// configured bind password.
func TestWarnCleartextBindOnceFiresOnce(t *testing.T) {
	var buf bytes.Buffer
	d := &Directory{
		cfg: Config{URL: "ldap://x", BindDN: "cn=svc", BindPassword: "secret"},
		log: slog.New(slog.NewTextHandler(&buf, nil)),
	}
	d.warnCleartextBindOnce()
	d.warnCleartextBindOnce()
	d.warnCleartextBindOnce()

	out := buf.String()
	if n := strings.Count(out, "cleartext"); n != 1 {
		t.Fatalf("warning logged %d times, want exactly 1:\n%s", n, out)
	}
	if !strings.Contains(out, "ldaps://") {
		t.Errorf("warning does not recommend ldaps://: %s", out)
	}
}

// TestWarnCleartextBindOnceSkipsSafeConfigs confirms the warning never fires
// when TLS is in use or no credentials would actually be sent.
func TestWarnCleartextBindOnceSkipsSafeConfigs(t *testing.T) {
	safe := []Config{
		{URL: "ldaps://x", BindDN: "cn=svc", BindPassword: "secret"}, // TLS
		{URL: "ldap://x", BindDN: "cn=svc", BindPassword: ""},        // no password sent
		{URL: "ldap://x", BindDN: "", BindPassword: "secret"},        // no bind at all
	}
	for _, cfg := range safe {
		var buf bytes.Buffer
		d := &Directory{cfg: cfg, log: slog.New(slog.NewTextHandler(&buf, nil))}
		d.warnCleartextBindOnce()
		if buf.Len() != 0 {
			t.Errorf("cfg=%+v: unexpected warning logged: %s", cfg, buf.String())
		}
	}
}

func TestNewDefaultsLogger(t *testing.T) {
	d, err := New(Config{URL: "ldaps://x", BaseDN: "dc=x"})
	if err != nil {
		t.Fatal(err)
	}
	if d.log == nil {
		t.Fatal("New did not default the logger")
	}
}
