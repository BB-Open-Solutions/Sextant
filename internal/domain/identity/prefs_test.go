package identity

import "testing"

func TestPreferencesValidate(t *testing.T) {
	good := []Preferences{
		{},
		{Timezone: "Europe/Amsterdam"},
		{Locale: "nl"},
		{Timezone: "UTC", Locale: "en"},
	}
	for _, p := range good {
		if err := p.Validate(); err != nil {
			t.Errorf("%+v rejected: %v", p, err)
		}
	}
	bad := []Preferences{
		{Timezone: "Mars/Olympus"},
		{Timezone: "CEST"},
		{Locale: "de"},
		{Locale: "EN"},
	}
	for _, p := range bad {
		if err := p.Validate(); err == nil {
			t.Errorf("%+v accepted", p)
		}
	}
}
