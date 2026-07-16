package web

import (
	"fmt"
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

func webAuthor(v view) ports.Author {
	email := v.User.Email
	if email == "" {
		email = v.User.Subject + "@idp"
	}
	return ports.Author{Subject: v.User.Subject, Name: v.User.Name, Email: email}
}

// parseValue interprets a form value: booleans and integers become typed,
// everything else stays a string.
func parseValue(s string) any {
	switch s {
	case "true":
		return true
	case "false":
		return false
	}
	var n int
	if _, err := fmt.Sscanf(s, "%d", &n); err == nil && fmt.Sprint(n) == s {
		return n
	}
	return s
}

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
