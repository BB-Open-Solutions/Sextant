package web

import (
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

func TestLocalizerResolution(t *testing.T) {
	// User preference wins over org default.
	l := newLocalizer(identity.Preferences{Locale: "nl", Timezone: "Europe/Amsterdam"}, "en", "UTC")
	if l.T("nav.devices") != "Apparaten" || l.Locale() != "nl" {
		t.Fatalf("nl = %q", l.T("nav.devices"))
	}
	// Empty prefs inherit org defaults.
	l = newLocalizer(identity.Preferences{}, "nl", "Europe/Amsterdam")
	if l.T("nav.devices") != "Apparaten" {
		t.Fatal("org default locale not applied")
	}
	// Unknown locale and zone fall back safely.
	l = newLocalizer(identity.Preferences{Locale: "de", Timezone: "Mars/Olympus"}, "xx", "Nope/Nope")
	if l.Locale() != "en" || l.Time(time.Unix(0, 0)) != "1970-01-01 00:00" {
		t.Fatalf("fallback = %s %s", l.Locale(), l.Time(time.Unix(0, 0)))
	}
	// Missing key: EN fallback, then the key itself.
	if newLocalizer(identity.Preferences{Locale: "nl"}, "en", "UTC").T("no.such.key") != "no.such.key" {
		t.Fatal("missing key not surfaced")
	}
}

func TestLocalizerTimezone(t *testing.T) {
	l := newLocalizer(identity.Preferences{Timezone: "Europe/Amsterdam"}, "en", "UTC")
	// July: CEST = UTC+2.
	utc := time.Date(2026, 7, 10, 12, 0, 0, 0, time.UTC)
	if got := l.Time(utc); got != "2026-07-10 14:00" {
		t.Fatalf("tz render = %s", got)
	}
	if l.Time(time.Time{}) != "-" || l.TimePtr(nil) != "-" {
		t.Fatal("zero/nil time not dashed")
	}
}

// TestLocalizerProgress: the wave counters read as a sentence in both
// languages, and both grammatical numbers are correct.
func TestLocalizerProgress(t *testing.T) {
	en := newLocalizer(identity.Preferences{Locale: "en"}, "en", "UTC")
	nl := newLocalizer(identity.Preferences{Locale: "nl"}, "en", "UTC")
	cases := []struct {
		name                      string
		onTarget, present, absent int
		wantEN, wantNL            string
	}{
		{"nothing known", 0, 0, 0, "", ""},
		{"only absent, singular", 0, 0, 1, "1 device not reporting", "1 apparaat niet bereikbaar"},
		{"none done yet", 0, 3, 0, "waiting on 3 devices", "wacht op 3 apparaten"},
		{"one still out", 2, 3, 0, "2 of 3 devices updated", "2 van 3 apparaten bijgewerkt"},
		{"all done", 3, 3, 0, "all 3 devices updated", "alle 3 apparaten bijgewerkt"},
		{"partly done with absentees", 1, 3, 2, "1 of 3 devices updated · 2 devices not reporting",
			"1 van 3 apparaten bijgewerkt · 2 apparaten niet bereikbaar"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := en.Progress(tc.onTarget, tc.present, tc.absent); got != tc.wantEN {
				t.Errorf("en = %q, want %q", got, tc.wantEN)
			}
			if got := nl.Progress(tc.onTarget, tc.present, tc.absent); got != tc.wantNL {
				t.Errorf("nl = %q, want %q", got, tc.wantNL)
			}
		})
	}
}

func TestCatalogParity(t *testing.T) {
	// Every NL key must exist in EN: EN is the source language, and an
	// NL-only key would mask a missing English string.
	for key := range catalog["nl"] {
		if _, ok := catalog["en"][key]; !ok {
			t.Errorf("nl key %q missing from en", key)
		}
	}
	for key := range catalog["en"] {
		if _, ok := catalog["nl"][key]; !ok {
			t.Errorf("en key %q not translated in nl", key)
		}
	}
}
