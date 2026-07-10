package ldap

import "testing"

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
