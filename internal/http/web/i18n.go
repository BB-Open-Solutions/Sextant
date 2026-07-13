package web

import (
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// i18n.go: the console's message catalog and per-user presentation.
// English is the source language and the fallback; NL translates the
// operator-facing chrome. The API always speaks English. A localizer is
// built per request from the user's preferences (organisation defaults
// as fallback) and handed to templates as .L.

// Localizer renders text and time for one user. Immutable per request.
type Localizer struct {
	locale string
	loc    *time.Location
}

// newLocalizer resolves preferences against organisation defaults. Both
// preference fields were validated at write time; a zone that fails to
// load anyway (tzdata drift) falls back to the default, never panics.
func newLocalizer(p identity.Preferences, defaultLocale, defaultTZ string) Localizer {
	locale := p.Locale
	if locale == "" {
		locale = defaultLocale
	}
	if _, ok := catalog[locale]; !ok {
		locale = "en"
	}
	tz := p.Timezone
	if tz == "" {
		tz = defaultTZ
	}
	loc, err := time.LoadLocation(tz)
	if err != nil || loc == nil {
		loc = time.UTC
	}
	return Localizer{locale: locale, loc: loc}
}

// T translates a catalog key.
func (l Localizer) T(key string) string {
	if s, ok := catalog[l.locale][key]; ok {
		return s
	}
	if s, ok := catalog["en"][key]; ok {
		return s
	}
	return key
}

// Time renders a timestamp in the user's zone, minute precision.
func (l Localizer) Time(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(l.loc).Format("2006-01-02 15:04")
}

// TimeSec renders with second precision (audit trail).
func (l Localizer) TimeSec(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(l.loc).Format("2006-01-02 15:04:05")
}

// Date renders a date only (token expiry).
func (l Localizer) Date(t time.Time) string {
	if t.IsZero() {
		return "-"
	}
	return t.In(l.loc).Format("2006-01-02")
}

// Locale exposes the resolved locale (lang attribute).
func (l Localizer) Locale() string { return l.locale }

// TimePtr renders an optional timestamp; nil is a dash.
func (l Localizer) TimePtr(t *time.Time) string {
	if t == nil {
		return "-"
	}
	return l.Time(*t)
}
