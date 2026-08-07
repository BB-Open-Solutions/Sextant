package web

import (
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

func sortedKeys[V any](m map[string]V) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
