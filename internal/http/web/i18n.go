package web

import (
	"strconv"
	"strings"
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

// devices renders a device count with the matching noun. The catalog is a
// flat key/value table with no pluralisation engine, so singular and plural
// are two keys.
func (l Localizer) devices(n int) string {
	key := "common.device_many"
	if n == 1 {
		key = "common.device_one"
	}
	return strconv.Itoa(n) + " " + l.T(key)
}

// Progress renders a wave's device counts as a sentence instead of the
// "(3/12 - 1 away)" arithmetic the board used to show: an operator reads how
// far the wave got and what it is still waiting on. Absent devices (silent
// beyond the window) are named separately because they left the promotion
// denominator - present is what the counts measure against.
func (l Localizer) Progress(onTarget, present, absent int) string {
	var parts []string
	switch remaining := present - onTarget; {
	case present <= 0: // nothing left to measure against; only absent remains
	case remaining <= 0:
		parts = append(parts, l.T("rollout.progress_all")+" "+l.devices(present)+" "+l.T("rollout.progress_updated"))
	case onTarget == 0:
		parts = append(parts, l.T("rollout.waiting_on")+" "+l.devices(remaining))
	default:
		parts = append(parts, strconv.Itoa(onTarget)+" "+l.T("rollout.progress_of")+" "+l.devices(present)+" "+l.T("rollout.progress_updated"))
	}
	if absent > 0 {
		parts = append(parts, l.devices(absent)+" "+l.T("rollout.not_reporting"))
	}
	return strings.Join(parts, " · ")
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
