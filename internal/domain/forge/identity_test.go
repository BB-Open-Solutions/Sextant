package forge

import "testing"

// The rules here are the only thing standing between a pasted credential and
// a netrc line that means something other than it looks like. netrc is
// whitespace-and-newline structured with no escaping at all, so a value
// carrying either does not fail - it reparses.

func TestValidateRefusesWhatWouldChangeTheNetrcLine(t *testing.T) {
	cases := []struct{ name, host, user, token string }{
		{"empty host", "", "bot", "t"},
		{"host as a URL", "https://forge.example.org", "bot", "t"},
		{"host with a path", "forge.example.org/dawo", "bot", "t"},
		{"host with a query", "forge.example.org?x=1", "bot", "t"},
		{"host with a fragment", "forge.example.org#x", "bot", "t"},
		{"host with a space", "forge example.org", "bot", "t"},
		{"host with a newline", "forge.example.org\nmachine evil", "bot", "t"},
		{"host with a tab", "forge\t.example.org", "bot", "t"},
		{"empty user", "forge.example.org", "", "t"},
		{"user with a space", "forge.example.org", "bo t", "t"},
		{"user with a newline", "forge.example.org", "bot\nlogin root", "t"},
		{"empty token", "forge.example.org", "bot", ""},
		// The one that matters most: a newline ends the line early and the
		// remainder becomes fresh netrc directives, so a pasted token with a
		// trailing line could add a second machine entry.
		{"token with a newline", "forge.example.org", "bot", "t\nmachine evil.example login a password b"},
		{"token with a carriage return", "forge.example.org", "bot", "t\rmachine evil.example"},
		// netrc is parsed as whitespace-separated words, so a plain space is
		// already enough: git would take the password as everything up to it
		// and read the rest as a directive.
		{"token with a space", "forge.example.org", "bot", "two words"},
		{"token with a tab", "forge.example.org", "bot", "two\twords"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.host, c.user, c.token); err == nil {
				t.Errorf("accepted host=%q user=%q token=%q", c.host, c.user, c.token)
			}
		})
	}
}

func TestValidateAcceptsOrdinaryCredentials(t *testing.T) {
	cases := []struct{ name, host, user, token string }{
		{"plain", "forge.example.org", "sextant-bot", "abc123"},
		{"host with a port", "forge.example.org:3000", "sextant-bot", "abc"},
		{"leading and trailing space on the host", "  forge.example.org  ", "bot", "abc"},
		// A token may legitimately contain punctuation that looks alarming;
		// only whitespace and line breaks change the netrc line's meaning.
		{"token with punctuation", "forge.example.org", "bot", "ghp_a-b_c.d/e+f="},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if err := Validate(c.host, c.user, c.token); err != nil {
				t.Errorf("refused a usable credential: %v", err)
			}
		})
	}
}

func TestNetrcRendersOneCompleteLine(t *testing.T) {
	got := Netrc("forge.example.org", "sextant-bot", "s3cr3t")
	want := "machine forge.example.org login sextant-bot password s3cr3t\n"
	if got != want {
		t.Errorf("Netrc() = %q, want %q", got, want)
	}
	// The trailing newline is not cosmetic: git's netrc parser wants a
	// terminated line, and a file whose last line is unterminated is a
	// credential that silently does not apply.
	if got[len(got)-1] != '\n' {
		t.Error("the netrc line is not terminated")
	}
}
