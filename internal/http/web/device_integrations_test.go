package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func fleetWith(settings map[string]any) *fleet.Fleet {
	return &fleet.Fleet{
		Version: 3,
		Org:     &fleet.Scope{Settings: settings},
		Devices: map[string]fleet.Device{"lt-1": {Hardware: "hw"}},
	}
}

// Only integrations the fleet switched on get a row. A fleet that carries a
// Wazuh manager address but never enabled Wazuh would otherwise report "no
// reading" on every device, which is a false alarm and not a finding.
func TestDeviceIntegrationsListsOnlyEnabled(t *testing.T) {
	f := fleetWith(map[string]any{
		"netbird.enable": true,
		"wazuh.manager":  "siem.example.org",
		"wazuh.enable":   false,
	})
	st := app.StatusView{DeviceStatus: observed.DeviceStatus{
		Tag:          "lt-1",
		Integrations: observed.Integrations{"netbird": {State: "up"}},
	}}
	rows := deviceIntegrations(f, "lt-1", st)
	if len(rows) != 1 {
		t.Fatalf("rows = %+v, want only the enabled integration", rows)
	}
	if !rows[0].Up() || rows[0].Down() || rows[0].Unmeasured() {
		t.Fatalf("netbird row = %+v, want up", rows[0])
	}
}

// The state the device reports is the state shown, including its own words on
// a failure - that detail is the difference between "the mesh is down" and a
// name an operator can act on.
func TestDeviceIntegrationsShowsReportedState(t *testing.T) {
	f := fleetWith(map[string]any{"netbird.enable": true, "wazuh.enable": true})
	st := app.StatusView{DeviceStatus: observed.DeviceStatus{
		Tag: "lt-1",
		Integrations: observed.Integrations{
			"netbird": {State: "down", Detail: "netbird.service failed"},
		},
	}}
	rows := deviceIntegrations(f, "lt-1", st)
	if len(rows) != 2 {
		t.Fatalf("rows = %d, want both enabled integrations", len(rows))
	}
	if !rows[0].Down() || rows[0].Detail != "netbird.service failed" {
		t.Fatalf("netbird row = %+v, want down with its detail", rows[0])
	}
	// Wazuh is on but the device said nothing about it. That is unmeasured,
	// and unmeasured is not down: an old agent must not make a working SIEM
	// agent look broken.
	if !rows[1].Unmeasured() || rows[1].Down() || rows[1].Up() {
		t.Fatalf("wazuh row = %+v, want unmeasured", rows[1])
	}
}

// nil (no agent ever reported) and empty (reported, nothing to say) are
// different answers, and the column is nullable exactly to keep them apart.
func TestReportedIntegrationsSeparatesSilenceFromEmpty(t *testing.T) {
	silent := app.StatusView{DeviceStatus: observed.DeviceStatus{Tag: "lt-1"}}
	if reportedIntegrations(silent) {
		t.Fatal("a device that never reported counts as having reported")
	}
	empty := app.StatusView{DeviceStatus: observed.DeviceStatus{
		Tag: "lt-1", Integrations: observed.Integrations{}}}
	if !reportedIntegrations(empty) {
		t.Fatal("a device that reported an empty set counts as silent")
	}
}

// osquery is the integration where "configured" and "running" come apart most
// expensively: a device meant to report to Fleet but not reporting is not
// visibly broken anywhere, it is simply absent from the estate view. So the
// row has to exist the moment the fleet turns it on, and say nothing rather
// than "down" when the device has not reported.
func TestOsqueryIsReportedLikeAnyOtherIntegration(t *testing.T) {
	f := fleetWith(map[string]any{"osquery.enable": true})

	silent := deviceIntegrations(f, "lt-1", app.StatusView{})
	if len(silent) != 1 {
		t.Fatalf("rows = %+v, want an osquery row as soon as the fleet enables it", silent)
	}
	if !silent[0].Unmeasured() || silent[0].Down() {
		t.Errorf("a device that has not reported reads as %+v; absent from Fleet is not "+
			"the same as broken, and must not be shown as down", silent[0])
	}

	running := deviceIntegrations(f, "lt-1", app.StatusView{DeviceStatus: observed.DeviceStatus{
		Tag:          "lt-1",
		Integrations: observed.Integrations{"osquery": {State: "up"}},
	}})
	if len(running) != 1 || !running[0].Up() {
		t.Fatalf("rows = %+v, want the reported up state", running)
	}
}
