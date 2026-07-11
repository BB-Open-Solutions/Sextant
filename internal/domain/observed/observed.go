// Package observed is the pure domain of the observed plane: what devices
// report about themselves (check-ins, deployed revision, hardware facts),
// as opposed to the config plane (what they should run). Storage lives
// behind ports; health rules live here so they are testable and uniform.
package observed

import (
	"fmt"
	"time"
)

// Phase is where a device is in its lifecycle, self-reported.
type Phase string

const (
	// Discovered: seen by an imaging station, not yet installed.
	Discovered Phase = "discovered"
	// Installing: imaging in progress.
	Installing Phase = "installing"
	// Installed: image on disk, not yet converging config.
	Installed Phase = "installed"
	// Running: converging via comin; the steady state.
	Running Phase = "running"
)

// Valid reports whether p is a known phase.
func (p Phase) Valid() bool {
	switch p {
	case Discovered, Installing, Installed, Running:
		return true
	}
	return false
}

// CheckIn is one device report. Tag identifies the device; Revision is the
// config revision it runs; Error carries a self-reported failure (e.g. a
// comin deploy error).
type CheckIn struct {
	Tag      string `json:"tag"`
	Revision string `json:"revision"`
	Phase    Phase  `json:"phase"`
	Error    string `json:"error,omitempty"`
	// SB and TPM2 are the observed security posture (design 0001);
	// empty means the agent did not report it (old agent / probe failed).
	SB   SBState   `json:"sb,omitempty"`
	TPM2 TPM2State `json:"tpm2,omitempty"`
	// Ack echoes a remote-action intent the device has executed
	// ("lock"/"wipe"), so the console can show delivered vs armed
	// (design 0004). Empty on an ordinary beat.
	Ack string `json:"ack,omitempty"`
}

// Validate rejects malformed check-ins before they reach storage.
func (c CheckIn) Validate() error {
	if c.Tag == "" {
		return fmt.Errorf("check-in needs a device tag")
	}
	if len(c.Tag) > 63 {
		return fmt.Errorf("device tag too long")
	}
	if c.Phase != "" && !c.Phase.Valid() {
		return fmt.Errorf("unknown phase %q", c.Phase)
	}
	if len(c.Revision) > 128 || len(c.Error) > 4096 {
		return fmt.Errorf("check-in field too long")
	}
	switch c.Ack {
	case "", "lock", "wipe":
	default:
		return fmt.Errorf("unknown ack %q", c.Ack)
	}
	return validatePosture(c.SB, c.TPM2)
}

// DeviceStatus is the stored, per-device observed state.
type DeviceStatus struct {
	Tag      string    `json:"tag"`
	Revision string    `json:"revision,omitempty"`
	Phase    Phase     `json:"phase,omitempty"`
	Error    string    `json:"error,omitempty"`
	LastSeen time.Time `json:"lastSeen"`
	SB       SBState   `json:"sb,omitempty"`
	TPM2     TPM2State `json:"tpm2,omitempty"`
	// Ack is the last remote-action intent the device confirmed executing.
	Ack string `json:"ack,omitempty"`
}

// OnlineWindow is how recently a device must have checked in to count as
// online. Devices check in about every minute; three missed beats = offline.
const OnlineWindow = 3 * time.Minute

// Online reports whether the device checked in recently.
func (s DeviceStatus) Online(now time.Time) bool {
	return !s.LastSeen.IsZero() && now.Sub(s.LastSeen) <= OnlineWindow
}

// Healthy is the rollout health rule: on the target, recently seen, running,
// and not reporting an error. One rule, used by the convergence source and
// the UI alike.
func (s DeviceStatus) Healthy(target string, now time.Time) bool {
	return s.Revision == target &&
		s.Online(now) &&
		(s.Phase == "" || s.Phase == Running) &&
		s.Error == ""
}
