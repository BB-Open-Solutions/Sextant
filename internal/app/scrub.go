package app

import "regexp"

// scrub.go: credentials must not survive into a place people read.
//
// A change's rejection is stored in the database and rendered on the pipeline
// page, and its text is whatever git, nix or the gate-runner said. Git errors
// quote the remote they were talking to, and a remote can carry its
// credential in the URL (https://user:token@forge/repo.git) - a shape the
// console never writes itself, but an overlay repo, a stray `git remote
// set-url`, or an operator following a tutorial can. Once that lands in an
// error string it is in the store, on the page, and in the browser history of
// everybody who reviews the change.
//
// So the console assumes it can happen and removes it on the way in. The user
// half is kept: knowing WHICH account failed to push is the whole point of
// reading the error.

// urlCredential matches the password half of a URL userinfo section.
var urlCredential = regexp.MustCompile(`([a-zA-Z][a-zA-Z0-9+.\-]*://[^/\s:@]+):([^/\s@]+)@`)

// bearerToken matches an Authorization header value echoed into output.
var bearerToken = regexp.MustCompile(`(?i)(authorization:\s*(?:bearer|token|basic)\s+)\S+`)

// ScrubCredentials replaces secrets in free text with a marker, leaving
// everything an operator needs in order to act on the message.
func ScrubCredentials(s string) string {
	s = urlCredential.ReplaceAllString(s, "$1:***@")
	s = bearerToken.ReplaceAllString(s, "${1}***")
	return s
}
