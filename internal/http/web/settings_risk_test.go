package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// TestRiskMarkerFor pins the write side of the risk brake (design 0012): which
// saves carry the marker the rollout engine holds on, and - just as important -
// which do not, since a marker that fires too eagerly turns standing policy
// back into a button nobody stops pressing.
func TestRiskMarkerFor(t *testing.T) {
	cat, err := fleet.ParseCatalog([]byte(`[
	  {"name":"ssh.enable","type":"boolean","description":"d","default":false,"riskClass":"high"},
	  {"name":"diskUnlock.tpm2.enable","type":"boolean","description":"d","default":false,"riskClass":"high"},
	  {"name":"netbird.enable","type":"boolean","description":"d","default":false},
	  {"name":"netbird.managementUrl","type":"string","description":"d"},
	  {"name":"desktop","type":"string","description":"d"}
	]`))
	if err != nil {
		t.Fatal(err)
	}
	marked := " " + app.RiskHighMarker

	cases := []struct {
		name    string
		changes []app.SettingChange
		want    string
	}{
		{
			name: "a high-risk catalog option brakes the whole save",
			changes: []app.SettingChange{
				{Key: "desktop", RawValue: "gnome"},
				{Key: "ssh.enable", RawValue: "true"},
			},
			want: marked,
		},
		{
			name:    "turning an integration on brakes",
			changes: []app.SettingChange{{Key: "netbird.enable", RawValue: "true"}},
			want:    marked,
		},
		{
			name:    "turning an integration off brakes too",
			changes: []app.SettingChange{{Key: "netbird.enable", Clear: true}},
			want:    marked,
		},
		{
			name:    "an image-time key never brakes, high-risk or not",
			changes: []app.SettingChange{{Key: "diskUnlock.tpm2.enable", RawValue: "true"}},
			want:    "",
		},
		{
			name:    "an integration's other options are ordinary settings",
			changes: []app.SettingChange{{Key: "netbird.managementUrl", RawValue: "https://vpn"}},
			want:    "",
		},
		{
			name:    "a plain option flows by itself",
			changes: []app.SettingChange{{Key: "desktop", RawValue: "gnome"}},
			want:    "",
		},
		{
			name:    "a key the catalog does not publish cannot be judged high-risk",
			changes: []app.SettingChange{{Key: "not.in.catalog", RawValue: "x"}},
			want:    "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := riskMarkerFor(cat, tc.changes); got != tc.want {
				t.Errorf("riskMarkerFor = %q, want %q", got, tc.want)
			}
		})
	}
}
