package identity

import (
	"fmt"
	"time"
)

// Preferences are per-user presentation settings. They are user data (not
// fleet configuration): stored in the database, never in the overlay repo.
// Empty fields mean "use the organisation default".
type Preferences struct {
	// Timezone is an IANA zone name ("Europe/Amsterdam"); empty inherits.
	Timezone string `json:"timezone,omitempty"`
	// Locale is a supported UI language tag; empty inherits.
	Locale string `json:"locale,omitempty"`
}

// SupportedLocales lists the UI languages the console ships.
var SupportedLocales = []string{"en", "nl"}

// Validate rejects unknown zones and unsupported locales at write time, so
// a bad preference can never break rendering later.
func (p Preferences) Validate() error {
	if p.Timezone != "" {
		if _, err := time.LoadLocation(p.Timezone); err != nil {
			return fmt.Errorf("unknown timezone %q (IANA name expected)", p.Timezone)
		}
	}
	if p.Locale != "" {
		ok := false
		for _, l := range SupportedLocales {
			if p.Locale == l {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("unsupported locale %q (supported: %v)", p.Locale, SupportedLocales)
		}
	}
	return nil
}
