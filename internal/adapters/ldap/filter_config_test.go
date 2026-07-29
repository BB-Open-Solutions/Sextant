package ldap

import (
	"reflect"
	"strings"
	"testing"
)

// The two values spliced into the search filter come from deploy-time config
// and are deliberately NOT escaped - GroupFilter is a filter expression and
// NameAttr is an attribute name. So the risk they carry is not injection but
// silence: a typo yields a filter that parses and matches nothing, and the
// operator sees an empty group picker with no explanation anywhere. These
// tests pin the guard that turns that into a refusal to start.

func TestNewRejectsMalformedNameAttr(t *testing.T) {
	for _, attr := range []string{
		"cn)(objectClass=*", // filter metacharacters
		"cn cn",             // whitespace
		"1cn",               // must start with a letter
		"cn;binary",         // RFC options: valid LDAP, never needed for a group name
		"cn*",
	} {
		_, err := New(Config{URL: "ldap://d:389", BaseDN: "dc=x", NameAttr: attr})
		if err == nil {
			t.Fatalf("NameAttr %q accepted; a malformed attribute name must refuse to start", attr)
		}
		if !strings.Contains(err.Error(), "name attribute") {
			t.Fatalf("NameAttr %q: error does not name the offending setting: %v", attr, err)
		}
	}
}

func TestNewRejectsMalformedGroupFilter(t *testing.T) {
	for _, filter := range []string{
		"(objectClass=group",    // unbalanced
		"objectClass=group)",    // missing open
		"(&(objectClass=group)", // unbalanced compound
		"",                      // empty is impossible - it defaults - but an all-spaces value is not
		"   ",
	} {
		cfg := Config{URL: "ldap://d:389", BaseDN: "dc=x", GroupFilter: filter}
		_, err := New(cfg)
		if filter == "" {
			// Empty means "use the default", which must still be valid.
			if err != nil {
				t.Fatalf("empty GroupFilter should fall back to the default, got %v", err)
			}
			continue
		}
		if err == nil {
			t.Fatalf("GroupFilter %q accepted; an unparseable filter must refuse to start", filter)
		}
		if !strings.Contains(err.Error(), "group filter") {
			t.Fatalf("GroupFilter %q: error does not name the offending setting: %v", filter, err)
		}
	}
}

func TestNewAcceptsRealWorldFilters(t *testing.T) {
	// The three shapes actually deployed: OpenLDAP/lldap, AD, and a scoped
	// compound filter. A guard that rejected any of these would be worse than
	// no guard at all.
	for _, tc := range []struct{ filter, attr string }{
		{"(objectClass=groupOfNames)", "cn"},
		{"(objectClass=group)", "sAMAccountName"},
		{"(&(objectClass=group)(cn=dawo-*))", "displayName"},
	} {
		if _, err := New(Config{
			URL: "ldaps://d:636", BaseDN: "dc=x",
			GroupFilter: tc.filter, NameAttr: tc.attr,
		}); err != nil {
			t.Fatalf("rejected a valid deployment config %q/%q: %v", tc.filter, tc.attr, err)
		}
	}
}

// Config must carry no way to disable certificate verification. The field it
// used to have was never wired to any configuration surface, so it could not
// actually be switched on - but a dead knob that turns off TLS verification is
// an invitation to the next person who needs to "just test something", and
// this is what stops it coming back under any name.
func TestConfigHasNoSkipVerifyKnob(t *testing.T) {
	ty := reflect.TypeOf(Config{})
	for i := range ty.NumField() {
		name := strings.ToLower(ty.Field(i).Name)
		if strings.Contains(name, "insecure") || strings.Contains(name, "skipverify") {
			t.Fatalf("Config.%s reintroduces a way to weaken TLS verification; "+
				"a lab with a self-signed certificate points identity.tlsCaCert at its CA instead",
				ty.Field(i).Name)
		}
	}
}
