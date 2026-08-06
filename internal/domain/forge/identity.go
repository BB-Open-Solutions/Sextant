// Package forge holds the console's own credential for talking to the git
// forge (ADR 0022). It is deliberately a separate domain from tokens: those
// authenticate somebody TO Sextant, this one authenticates Sextant to
// something else, and conflating the two is how a console ends up pushing as
// whoever last logged in.
package forge

import (
	"fmt"
	"strings"
	"time"
)

// Identity is the forge account the console pushes with. TokenEnc is the
// sealed credential; there is deliberately no plaintext field, so a value
// read from the store cannot be logged or rendered by accident.
type Identity struct {
	Host      string
	Username  string
	TokenEnc  []byte
	Updated   time.Time
	UpdatedBy string
}

// Validate checks what can be checked without contacting the forge. It is
// intentionally strict about the host: a netrc machine line matches on the
// host alone, so a scheme or a path smuggled in here would produce a machine
// line that silently never matches and a push that silently falls back to
// whatever else is on disk.
func Validate(host, username, token string) error {
	host = strings.TrimSpace(host)
	switch {
	case host == "":
		return fmt.Errorf("forge host is required")
	case strings.ContainsAny(host, " \t\r\n"):
		return fmt.Errorf("forge host %q must not contain whitespace", host)
	case strings.Contains(host, "://"):
		return fmt.Errorf("forge host must be a hostname, not a URL (drop the %q)",
			host[:strings.Index(host, "://")+3])
	case strings.ContainsAny(host, "/?#"):
		return fmt.Errorf("forge host must be a hostname with no path")
	}
	if strings.TrimSpace(username) == "" {
		return fmt.Errorf("forge username is required")
	}
	if strings.ContainsAny(username, " \t\r\n") {
		return fmt.Errorf("forge username must not contain whitespace")
	}
	if token == "" {
		return fmt.Errorf("forge token is required")
	}
	// netrc has no escaping and no quoting worth relying on: it is parsed as
	// whitespace-separated words. So ANY whitespace in the token, not only a
	// line break, changes what the line means - "password two words" leaves
	// git with the password "two" and a stray directive after it, which
	// authenticates as nothing and reads as a wrong password. A line break is
	// the worst case (the remainder becomes fresh netrc directives, so a
	// pasted value could add a second machine entry), but a plain space is
	// already enough to break it silently.
	//
	// Refuse rather than sanitise: a token that needs sanitising is a token
	// that was pasted wrong, and quietly trimming it would hand somebody a
	// credential that is not the one they hold.
	if strings.ContainsAny(token, " \t\r\n\v\f") {
		return fmt.Errorf("forge token must not contain whitespace or a line break")
	}
	return nil
}

// Netrc renders the single machine line git reads. Kept here, next to
// Validate, so the rules that make the line safe and the code that writes it
// cannot drift apart.
func Netrc(host, username, token string) string {
	return fmt.Sprintf("machine %s login %s password %s\n", host, username, token)
}
