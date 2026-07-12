package ldap

import (
	"testing"

	ldapv3 "github.com/go-ldap/ldap/v3"
)

func TestMapGroups(t *testing.T) {
	entries := []*ldapv3.Entry{
		ldapv3.NewEntry("cn=admins,ou=groups,dc=x", map[string][]string{"cn": {"admins"}}),
		ldapv3.NewEntry("cn=noname,ou=groups,dc=x", map[string][]string{"cn": {}}), // skipped
		ldapv3.NewEntry("cn=devs,ou=groups,dc=x", map[string][]string{"cn": {"devs"}}),
	}
	got := mapGroups(entries, "cn")
	if len(got) != 2 {
		t.Fatalf("mapGroups len = %d, want 2 (nameless skipped)", len(got))
	}
	if got[0].Name != "admins" || got[0].ID != "cn=admins,ou=groups,dc=x" {
		t.Fatalf("first group wrong: %+v", got[0])
	}
	if got[1].Name != "devs" {
		t.Fatalf("second group wrong: %+v", got[1])
	}
}
