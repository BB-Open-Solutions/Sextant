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

// catalog maps locale -> key -> translation. A missing key falls back to
// English; a missing English key renders the key itself, which makes an
// untranslated string visible instead of silently blank.
var catalog = map[string]map[string]string{
	"en": {
		"nav.overview": "Overview",
		"nav.devices":  "Devices",
		"nav.groups":   "Groups",
		"nav.settings": "Settings",
		"nav.policies": "Policies",
		"nav.changes":  "Changes",
		"nav.rollout":  "Rollout",
		"nav.access":   "Access",
		"nav.audit":    "Audit",
		"nav.signout":  "Sign out",

		"common.apply":   "Apply",
		"common.save":    "Save",
		"common.create":  "Create",
		"common.remove":  "Remove",
		"common.revoke":  "Revoke",
		"common.online":  "online",
		"common.offline": "offline",
		"common.never":   "never",

		"device.retired":    "retired",
		"device.retire":     "Retire",
		"device.reactivate": "Reactivate",

		"secret.store_now": "Store this secret now; it is not shown again.",
	},
	"nl": {
		"nav.overview": "Overzicht",
		"nav.devices":  "Apparaten",
		"nav.groups":   "Groepen",
		"nav.settings": "Instellingen",
		"nav.policies": "Beleid",
		"nav.changes":  "Wijzigingen",
		"nav.rollout":  "Uitrol",
		"nav.access":   "Toegang",
		"nav.audit":    "Audit",
		"nav.signout":  "Uitloggen",

		"common.apply":   "Toepassen",
		"common.save":    "Opslaan",
		"common.create":  "Aanmaken",
		"common.remove":  "Verwijderen",
		"common.revoke":  "Intrekken",
		"common.online":  "online",
		"common.offline": "offline",
		"common.never":   "nooit",

		"device.retired":    "uitgefaseerd",
		"device.retire":     "Uitfaseren",
		"device.reactivate": "Heractiveren",

		"secret.store_now": "Bewaar dit geheim nu; het wordt niet nog eens getoond.",
	},
}

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
