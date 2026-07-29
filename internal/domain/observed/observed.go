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
	// Discovered means seen by an imaging station, not yet installed.
	Discovered Phase = "discovered"
	// Installing means imaging is in progress.
	Installing Phase = "installing"
	// Installed means the image is on disk, not yet converging config.
	Installed Phase = "installed"
	// Running means converging via comin; the steady state.
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
	// Ack reports the OUTCOME of a remote-action intent (design 0004), so the
	// console shows what the root executor actually did, not merely that the
	// agent spooled the request: the bare intent name means it executed, and
	// the -refused/-failed variants mean it was declined (unarmed host / lock
	// interlock) or could not finish. Empty on an ordinary beat.
	Ack string `json:"ack,omitempty"`
	// Usage is the device's live resource utilisation at this beat (optional).
	Usage Usage `json:"usage,omitempty"`
}

// Usage is a device's live resource utilisation at check-in: a snapshot, not a
// series. All-zero means the agent did not report it (an older agent or a
// failed probe), so the console shows it as unknown rather than 0%.
type Usage struct {
	CPUPct      int `json:"cpuPct,omitempty"` // 0..100 over the sample window
	MemUsedMB   int `json:"memUsedMB,omitempty"`
	MemTotalMB  int `json:"memTotalMB,omitempty"`
	DiskUsedGB  int `json:"diskUsedGB,omitempty"`
	DiskTotalGB int `json:"diskTotalGB,omitempty"`
}

// Reported is true once the device has sent any utilisation figure, so the
// console can distinguish "0%" from "not reported".
func (u Usage) Reported() bool {
	return u.CPUPct > 0 || u.MemTotalMB > 0 || u.DiskTotalGB > 0
}

// validateUsage bounds the utilisation fields so a bad agent cannot store
// nonsense (a negative or >100 CPU, used exceeding total).
func validateUsage(u Usage) error {
	if u.CPUPct < 0 || u.CPUPct > 100 {
		return fmt.Errorf("cpu%% %d out of range 0..100", u.CPUPct)
	}
	if u.MemUsedMB < 0 || u.MemTotalMB < 0 || u.DiskUsedGB < 0 || u.DiskTotalGB < 0 {
		return fmt.Errorf("usage fields must be non-negative")
	}
	if u.MemUsedMB > u.MemTotalMB && u.MemTotalMB > 0 {
		return fmt.Errorf("memory used %d exceeds total %d", u.MemUsedMB, u.MemTotalMB)
	}
	if u.DiskUsedGB > u.DiskTotalGB && u.DiskTotalGB > 0 {
		return fmt.Errorf("disk used %d exceeds total %d", u.DiskUsedGB, u.DiskTotalGB)
	}
	return nil
}

// Remote-action ack outcomes.
const (
	AckLock        = "lock"         // session lock carried out
	AckWipe        = "wipe"         // crypto-wipe carried out
	AckWipeRefused = "wipe-refused" // executor declined (unarmed / interlock)
	AckWipeFailed  = "wipe-failed"  // erase attempted but did not complete
	AckRebooted    = "rebooted"     // operator-requested reboot completed

	// Provisioning-ceremony outcomes (design 0004, wizard). The executor
	// reports the milestone it just carried out; the console advances the
	// device's image job on them, so the wizard reflects what HAPPENED on
	// the device, never what was merely requested.
	AckSBEnrolled       = "sb-enrolled"        // platform keys enrolled; SB active next boot
	AckSBEnrollFailed   = "sb-enroll-failed"   // sbctl enroll-keys failed
	AckTPM2Enrolled     = "tpm2-enrolled"      // LUKS keyslot sealed to the TPM2 (PCR 7)
	AckTPM2EnrollFailed = "tpm2-enroll-failed" // systemd-cryptenroll failed

	// Diagnostics collection outcomes (design 0010).
	AckDiagnostics       = "diagnostics"        // bundle collected; upload follows
	AckDiagnosticsFailed = "diagnostics-failed" // collection failed on the device
)

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
	case "", AckLock, AckWipe, AckWipeRefused, AckWipeFailed, AckRebooted,
		AckSBEnrolled, AckSBEnrollFailed, AckTPM2Enrolled, AckTPM2EnrollFailed,
		AckDiagnostics, AckDiagnosticsFailed:
	default:
		return fmt.Errorf("unknown ack %q", c.Ack)
	}
	if err := validateUsage(c.Usage); err != nil {
		return err
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
	// Usage is the device's last-reported live resource utilisation.
	Usage Usage `json:"usage,omitempty"`
}

// OnlineWindow is how recently a device must have checked in to count as
// online. Devices check in about every minute; three missed beats = offline.
const OnlineWindow = 3 * time.Minute

// InactiveWindow is how long a device may stay offline before that becomes
// an operator concern. Offline itself is normal for laptops (weekends,
// vacation - operator decision 2026-07-29, matching the Intune/FleetDM
// idiom where offline is a neutral state and only prolonged absence
// escalates); two weeks covers a vacation, past it the machine may be
// lost, broken or shelved.
const InactiveWindow = 14 * 24 * time.Hour

// AbsentWindow is how long a device must have been silent before a rollout
// stops WAITING for it. A laptop can be shut for days or a week (holiday) -
// normal life, not a failure - so an absent device leaves the promotion
// denominator and catches up on its next check-in (the ring branch already
// points at the release). Distinct from OnlineWindow: minutes of silence
// make a device unhealthy, only prolonged silence makes it absent.
const AbsentWindow = time.Hour

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
