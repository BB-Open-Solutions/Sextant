package web

import (
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// device_integrations.go joins what the fleet turned on for a device with what
// the device says it observed of it (issue #49).
//
// The gap this closes: the console configured NetBird, Wazuh and OpenBao and
// could then see nothing at all of them. Answering "is this laptop on the
// mesh" meant running `ip` and `ss` on the machine by hand, and the fit-gap
// called two of those integrations a GAP for a fortnight after they had
// started working, because nothing in the product contradicted it.

// integrationRow is one integration on the device page.
type integrationRow struct {
	Name string
	Icon string
	// State is "up", "down" or "" when the device reported nothing about
	// this integration.
	State string
	// Detail is the device's own words on a "down", e.g. which unit failed.
	Detail string
}

// Up and Down keep the template free of string comparison.
func (r integrationRow) Up() bool   { return r.State == "up" }
func (r integrationRow) Down() bool { return r.State == "down" }

// Unmeasured reports that the fleet turned this integration on and the device
// has not said anything about it. That is deliberately NOT the same as down:
// an old agent, or a probe that could not run, must not colour a working mesh
// red. The panel says "no reading" and leaves the verdict alone.
func (r integrationRow) Unmeasured() bool { return r.State == "" }

// deviceIntegrations returns a row per integration that is ON for this device,
// in the order of knownIntegrations. An integration the fleet did not turn on
// gets no row: the page shows what is configured, not a catalogue.
func deviceIntegrations(f *fleet.Fleet, tag string, st app.StatusView) []integrationRow {
	resolved := f.ResolveValues(tag)
	var out []integrationRow
	for _, ig := range knownIntegrations {
		if !integrationEnabled(resolved, ig.Prefix) {
			continue
		}
		obs := st.Integrations[ig.Key]
		out = append(out, integrationRow{
			Name:   ig.Name,
			Icon:   ig.Icon,
			State:  obs.State,
			Detail: obs.Detail,
		})
	}
	return out
}

// integrationEnabled reads the integration's own enable flag. Only that flag
// counts: a fleet may carry a server address for an integration it has not
// switched on yet, and showing "no reading" for something nobody enabled
// would be a false alarm on every device.
func integrationEnabled(resolved map[string]any, prefix string) bool {
	v, ok := resolved[prefix+"enable"]
	if !ok {
		return false
	}
	b, _ := v.(bool)
	return b
}

// reportedIntegrations reports whether the device ever sent an integration
// reading at all. A nil map means no agent on this device has ever reported
// (see 0019_device_integrations.sql); the panel then says it is waiting for a
// check-in rather than listing every integration as unmeasured, which reads
// like a fleet-wide outage.
func reportedIntegrations(st app.StatusView) bool {
	return st.Integrations != nil
}
