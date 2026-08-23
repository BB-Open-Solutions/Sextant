package main

import (
	"encoding/json"
	"os"
	"testing"
)

// The demo exists to show what the console can do. An integration the fleet
// never turned on gets no row on a device page, so a generated fleet that
// enables nothing hides the feature completely - which is exactly what it did
// until 2026-08-23.
func TestTheGeneratedFleetTurnsIntegrationsOn(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "fleet-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDemoFleet(f, 30); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Org struct {
			Settings map[string]any `json:"settings"`
		} `json:"org"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}

	// These are the keys the console reads to decide whether an integration
	// gets a row (integrationEnabled reads "<prefix>enable" and nothing else),
	// so the test names them rather than counting how many are set.
	for _, key := range []string{"netbird.enable", "wazuh.enable", "identity.enable"} {
		v, ok := doc.Org.Settings[key]
		if !ok {
			t.Errorf("%s missing: the device page will show no integrations at all", key)
			continue
		}
		if v != true {
			t.Errorf("%s = %v, want true", key, v)
		}
	}
}

// A demo where every reading is green teaches an operator nothing, and the
// reading that matters most is the absent one: a device that has not reported
// must read as unmeasured, never as broken.
func TestSimulatedIntegrationsShowAllThreeReadings(t *testing.T) {
	var up, down, silent int
	for _, tag := range demoTags(t, 400) {
		ig := simulateIntegrations(tag)
		if ig == nil {
			silent++
			continue
		}
		if ig["wazuh"]["state"] == "down" {
			down++
			if ig["wazuh"]["detail"] == "" {
				t.Errorf("%s: wazuh down with no detail, so the page can say it is broken but not why", tag)
			}
			continue
		}
		up++
	}

	if up == 0 {
		t.Error("no device reports a working integration")
	}
	if down == 0 {
		t.Error("no device reports a failing integration, so the down state is never shown")
	}
	if silent == 0 {
		t.Error("every device reports, so the unmeasured state - the one that must not read as broken - is never shown")
	}
}

// Same tag, same reading. A demo that reshuffles itself between runs makes a
// screenshot stop meaning what it meant, and makes a bug report unrepeatable.
func TestSimulatedIntegrationsAreStablePerDevice(t *testing.T) {
	for _, tag := range demoTags(t, 50) {
		a, b := simulateIntegrations(tag), simulateIntegrations(tag)
		if (a == nil) != (b == nil) {
			t.Fatalf("%s: reported on one call and not the other", tag)
		}
		for name, state := range a {
			for field, want := range state {
				if got := b[name][field]; got != want {
					t.Fatalf("%s/%s/%s: %q then %q", tag, name, field, want, got)
				}
			}
		}
	}
}

// demoTags returns the tags of a generated fleet, so the tests run against the
// names the demo actually produces rather than invented ones.
func demoTags(t *testing.T, n int) []string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "fleet-*.json")
	if err != nil {
		t.Fatal(err)
	}
	if err := writeDemoFleet(f, n); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Devices map[string]json.RawMessage `json:"devices"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	tags := make([]string, 0, len(doc.Devices))
	for tag := range doc.Devices {
		tags = append(tags, tag)
	}
	if len(tags) == 0 {
		t.Fatal("generated fleet has no devices")
	}
	return tags
}
