package web

import "testing"

func TestSlugify(t *testing.T) {
	cases := map[string]string{
		"identity.bindSecret": "identity-bindsecret",
		"netbird.setupKey":    "netbird-setupkey",
		"  ..A_b/C..  ":       "a-b-c",
		"":                    "",
	}
	for in, want := range cases {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}
