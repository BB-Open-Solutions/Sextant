package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

func TestSafeLocalPath(t *testing.T) {
	cases := map[string]string{
		"/changes/x":          "/changes/x",
		"":                    "/notifications",
		"//evil.example.com":  "/notifications", // protocol-relative open redirect
		"https://evil.com":    "/notifications",
		"javascript:alert(1)": "/notifications",
		`/\evil.com`:          "/notifications", // backslash normalizes to // in browsers
		`\\evil.com`:          "/notifications", // no leading '/' at all, and backslashes
		`/\/evil.com`:         "/notifications",
		"/":                   "/",
	}
	for in, want := range cases {
		if got := safeLocalPath(in); got != want {
			t.Errorf("safeLocalPath(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestNotifPresentKnownAndUnknownKinds(t *testing.T) {
	// A known kind gets its own icon.
	r := notifPresent(notify.Notification{Kind: notify.GateFailed, Title: "x"})
	if r.Icon != "gpp_bad" {
		t.Errorf("gate-failed icon = %q", r.Icon)
	}
	// An unknown kind still renders with the neutral bell.
	r = notifPresent(notify.Notification{Kind: notify.Kind("mystery"), Title: "x"})
	if r.Icon != "notifications" {
		t.Errorf("unknown kind icon = %q, want notifications", r.Icon)
	}
}
