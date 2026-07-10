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
