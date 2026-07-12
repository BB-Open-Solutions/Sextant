// Package discovery is the pure domain of the pre-enrollment plane: devices an
// imaging station (the inspoelstraat) has seen over PXE but that are not yet
// enrolled in the fleet. A discovery is keyed by MAC, not by a fleet tag - the
// device has no tag until an operator enrolls it. Storage lives behind a port;
// the validation rules live here so they are testable and uniform.
package discovery

import (
	"fmt"
	"regexp"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// macRE matches a canonical lower-case colon-separated MAC (aa:bb:cc:dd:ee:ff).
// The station normalises leases to this form; anything else is rejected rather
// than stored, so a malformed report cannot poison the discovered set.
var macRE = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)

// Bounds on a single reported field, applied before storage so a hostile or
// broken station cannot balloon a row. Facter is the largest (a full
// nixos-facter document) and gets its own, more generous, cap.
const (
	maxStringField = 256
	maxFacterBytes = 512 * 1024
	// MaxBatch bounds one report so a single call cannot enumerate an
	// unbounded set into memory.
	MaxBatch = 4096
)

// Discovered is one device an imaging station has seen but that is not yet
// enrolled. The hardware fields are best-effort: the station fills what the
// DHCP lease and (later) the booting installer expose; empty is normal.
type Discovered struct {
	MAC      string         `json:"mac"`
	Serial   string         `json:"serial,omitempty"`
	Vendor   string         `json:"vendor,omitempty"`
	Model    string         `json:"model,omitempty"`
	CPU      string         `json:"cpu,omitempty"`
	Cores    int            `json:"cores,omitempty"`
	MemGB    int            `json:"memGB,omitempty"`
	DiskGB   int            `json:"diskGB,omitempty"`
	Firmware string         `json:"firmware,omitempty"`
	Facter   string         `json:"facter,omitempty"` // raw nixos-facter JSON
	Phase    observed.Phase `json:"phase"`
	LastSeen time.Time      `json:"lastSeen"`
}

// NormalizeMAC lower-cases and trims a MAC so equal addresses compare equal
// regardless of how the station formatted them.
func NormalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}

// NormalizePhase maps a station's reported phase onto the domain vocabulary.
// A real imaging station reports the state it observes off the DHCP/PXE lease
// ("pxe" for a machine netbooting into the installer), which is exactly the
// pre-install Discovered phase - so we accept it as an alias rather than 400
// a well-behaved station. Unknown values pass through unchanged and are
// rejected by Validate, preserving the closed set.
func NormalizePhase(p observed.Phase) observed.Phase {
	switch observed.Phase(strings.ToLower(strings.TrimSpace(string(p)))) {
	case "pxe", "netboot", "seen":
		return observed.Discovered
	case observed.Discovered, observed.Installing, observed.Installed:
		return observed.Phase(strings.ToLower(strings.TrimSpace(string(p))))
	default:
		return p
	}
}

// Validate rejects a malformed discovery before it reaches storage. Only the
// pre-enrollment phases make sense here (a discovery is by definition not yet
// running its config), so Running is refused.
func (d Discovered) Validate() error {
	if !macRE.MatchString(d.MAC) {
		return fmt.Errorf("discovery needs a canonical MAC (aa:bb:cc:dd:ee:ff), got %q", d.MAC)
	}
	switch d.Phase {
	case observed.Discovered, observed.Installing, observed.Installed:
	case "":
		return fmt.Errorf("discovery needs a phase")
	default:
		return fmt.Errorf("phase %q is not valid before enrollment", d.Phase)
	}
	for name, v := range map[string]string{
		"serial": d.Serial, "vendor": d.Vendor, "model": d.Model,
		"cpu": d.CPU, "firmware": d.Firmware,
	} {
		if len(v) > maxStringField {
			return fmt.Errorf("discovery %s too long", name)
		}
	}
	if len(d.Facter) > maxFacterBytes {
		return fmt.Errorf("discovery facter document too large")
	}
	if d.Cores < 0 || d.MemGB < 0 || d.DiskGB < 0 {
		return fmt.Errorf("discovery hardware counts must not be negative")
	}
	return nil
}

// Report is a station's full set of currently-discovered devices. A report
// replaces the station's whole set (leases that vanished are gone), so it must
// be internally consistent: no duplicate MACs, every entry valid, bounded size.
type Report struct {
	Devices []Discovered `json:"devices"`
}

// Validate checks a whole report and normalises every MAC in place.
func (r *Report) Validate() error {
	if len(r.Devices) > MaxBatch {
		return fmt.Errorf("report has %d devices, over the %d limit", len(r.Devices), MaxBatch)
	}
	seen := make(map[string]struct{}, len(r.Devices))
	for i := range r.Devices {
		r.Devices[i].MAC = NormalizeMAC(r.Devices[i].MAC)
		r.Devices[i].Phase = NormalizePhase(r.Devices[i].Phase)
		if err := r.Devices[i].Validate(); err != nil {
			return err
		}
		if _, dup := seen[r.Devices[i].MAC]; dup {
			return fmt.Errorf("report lists MAC %s twice", r.Devices[i].MAC)
		}
		seen[r.Devices[i].MAC] = struct{}{}
	}
	return nil
}
