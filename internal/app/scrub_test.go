package app

import (
	"strings"
	"testing"
)

// A rejection is stored and then rendered on a page anybody reviewing the
// change can read. Git quotes the remote it was talking to, and a remote can
// carry its credential in the URL - so the console assumes that happens and
// removes it before the text is persisted.
func TestScrubCredentials(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"token in a push url",
			"git push https://release-bot:ghp_abc123XYZ@forge.example.com/bb-open/overlay.git: denied",
			"git push https://release-bot:***@forge.example.com/bb-open/overlay.git: denied"},
		{"user without a password is left alone",
			"To https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen.git",
			"To https://forgejo.bb-open.com/bb-open/sextant-overlay-bbopen.git"},
		{"ssh style url with a password",
			"ssh://git:secretpw@forge.example.com:2222/x.git unreachable",
			"ssh://git:***@forge.example.com:2222/x.git unreachable"},
		{"authorization header echoed back",
			"POST /build failed\nAuthorization: Bearer eyJhbGciOi.J9.sig\nstatus 401",
			"POST /build failed\nAuthorization: Bearer ***\nstatus 401"},
		{"ordinary gate output is untouched",
			"error: attribute 'foo' missing at /nix/store/abc-source/flake.nix:12:3",
			"error: attribute 'foo' missing at /nix/store/abc-source/flake.nix:12:3"},
		{"empty stays empty", "", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ScrubCredentials(c.in); got != c.want {
				t.Errorf("got  %q\nwant %q", got, c.want)
			}
		})
	}
}

// The scrubber must not eat the part that makes an error actionable: which
// account, which host, which repository.
func TestScrubKeepsWhatTheOperatorNeeds(t *testing.T) {
	got := ScrubCredentials("fatal: could not read from https://release-bot:tok@forge.example.com/bb-open/overlay.git")
	for _, want := range []string{"release-bot", "forge.example.com", "bb-open/overlay.git"} {
		if !strings.Contains(got, want) {
			t.Errorf("scrubbed away %q: %s", want, got)
		}
	}
	if strings.Contains(got, "tok@") {
		t.Errorf("the secret survived: %s", got)
	}
}
