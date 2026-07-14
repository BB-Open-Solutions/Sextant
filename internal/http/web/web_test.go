package web

import (
	"testing"
	"unicode/utf8"
)

func TestInitials(t *testing.T) {
	cases := map[string]string{
		"Jane Doe":     "JD",
		"jane":         "J",
		"":             "?",
		"   ":          "?",
		"Ömer Yılmaz":  "ÖY",
		"Łukasz Nowak": "ŁN",
		"田中太郎":         "田",
		"A B C":        "AB", // only the first two parts count
	}
	for name, want := range cases {
		got := initials(name)
		if got != want {
			t.Errorf("initials(%q) = %q, want %q", name, got, want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("initials(%q) = %q is not valid UTF-8", name, got)
		}
	}
}
