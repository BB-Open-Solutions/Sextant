package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

func TestRequiresOfConvention(t *testing.T) {
	cat, err := fleet.ParseCatalog([]byte(`[
	  {"name":"timesync.enable","type":"boolean","description":"d","default":false},
	  {"name":"timesync.options.servers","type":"list of string","description":"d"},
	  {"name":"diskUnlock.tpm2.enable","type":"boolean","description":"d","default":false},
	  {"name":"diskUnlock.tpm2.device","type":"string","description":"d"},
	  {"name":"desktop.plasma.enable","type":"boolean","description":"d","default":false},
	  {"name":"hostname","type":"string","description":"d"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]string{
		"timesync.options.servers": "timesync.enable",
		"diskUnlock.tpm2.device":   "diskUnlock.tpm2.enable",
		"timesync.enable":          "", // an enable depends on nothing
		"desktop.plasma.enable":    "",
		"hostname":                 "", // no enable anywhere up its prefix
	}
	for key, want := range cases {
		if got := requiresOf(cat, key); got != want {
			t.Errorf("requiresOf(%s) = %q, want %q", key, got, want)
		}
	}

	// effectiveBool: own beats resolved beats default.
	if effectiveBool(cat, map[string]any{"timesync.enable": true}, nil, "timesync.enable") != true {
		t.Error("own value ignored")
	}
	if effectiveBool(cat, nil, map[string]fleet.Resolution{
		"timesync.enable": {Value: true},
	}, "timesync.enable") != true {
		t.Error("resolved value ignored")
	}
	if effectiveBool(cat, nil, nil, "timesync.enable") != false {
		t.Error("catalog default ignored")
	}
}
