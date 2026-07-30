// Package imaging is the pure domain of the imaging-execution plane: an
// operator dispatches an image job for a discovered device, an imaging
// station polls its jobs and runs the install (nixos-anywhere), and reports
// progress back. It is intent-as-data, like device intents (ADR/design
// 0004): Sextant never drives the installer directly - it records what
// should happen and the station acts, so every image is an audited record
// and there is no live command channel into the imaging LAN.
//
// A job is not done at "installed": provisioning continues through the
// Secure Boot and TPM2 ceremony as a guided, batchable wizard. Which of those
// phases apply is decided by the device's resolved group policy (SB required?
// TPM2 required?); a job simply advances through the states that apply and
// stops at Done. Software steps (key generation, cryptenroll) are driven
// automatically by the station/agent; the few firmware toggles that need a
// human at the BIOS are surfaced as manual actions with brand-specific
// instructions and an operator-triggered reboot.
package imaging

import (
	"fmt"
	"regexp"
	"strings"
)

// LUKSRecoveryPrefix marks a device's LUKS recovery key inside the install
// status message a station reports. It is the station->console wire contract:
// the console seals the key into the secret store (and drops it from the
// message), or, with no secret store, keeps it for a one-shot copy. Shared here
// so the reporter, the API and the console never drift on the literal.
const LUKSRecoveryPrefix = "luks-recovery-key: "

// Status is where an image job is in its lifecycle.
type Status string

const (
	// Pending means dispatched by an operator, the station has not started yet.
	Pending Status = "pending"
	// Imaging means the station is running the install (nixos-anywhere).
	Imaging Status = "imaging"
	// Installed means the image is on disk; the device converges from here. If the
	// group policy needs neither Secure Boot nor TPM2 this is the last active
	// state before Done.
	Installed Status = "installed"
	// SBPending means installed, the Secure Boot ceremony is underway - keys are
	// generated/signed automatically, and the operator is walked through the
	// firmware toggles (SB -> setup/audit mode, then SB on) with reboots.
	SBPending Status = "sb-pending"
	// SBEnrolled means Secure Boot is active and enforcing on the device.
	SBEnrolled Status = "sb-enrolled"
	// TPM2Enrolled means the LUKS volume is sealed to the TPM2 (PCR7); the device
	// unlocks without a passphrase.
	TPM2Enrolled Status = "tpm2-enrolled"
	// Done means fully provisioned, steady state. Terminal.
	Done Status = "done"
	// Failed means a step failed; Message carries the reason. Retryable.
	Failed Status = "failed"
	// Canceled means the operator withdrew the job before it completed. Terminal.
	Canceled Status = "canceled"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case Pending, Imaging, Installed, SBPending, SBEnrolled, TPM2Enrolled, Done, Failed, Canceled:
		return true
	}
	return false
}

// Terminal reports whether no further transition is expected.
func (s Status) Terminal() bool { return s == Done || s == Canceled }

// Phase groups the lifecycle into the three wizard phases (plus a terminal
// bucket), for the batch stepper and per-device grouping in the UI.
func (s Status) Phase() string {
	switch s {
	case Pending, Imaging, Installed:
		return "install"
	case SBPending, SBEnrolled:
		return "secureboot"
	case TPM2Enrolled:
		return "tpm2"
	case Done:
		return "done"
	default: // Failed, Canceled
		return "halted"
	}
}

// CanTransition reports whether a job may move from s to to.
//
// The station drives pending->imaging->installed and may fail from either
// active state. From installed the job branches by the device's resolved
// policy: to sb-pending when Secure Boot is required, straight to tpm2-enrolled
// when only TPM2 is, or straight to done when neither is - the caller decides
// which by what it reports. The SB ceremony (sb-pending->sb-enrolled) and TPM2
// sealing (->tpm2-enrolled) advance likewise. An operator may cancel any job
// that has not finished and may retry a failed job back to pending. Nothing
// leaves a terminal state (done, canceled).
func (s Status) CanTransition(to Status) bool {
	if !to.Valid() {
		return false
	}
	switch s {
	case Pending:
		return to == Imaging || to == Failed || to == Canceled
	case Imaging:
		return to == Installed || to == Failed || to == Canceled
	case Installed:
		// SBEnrolled directly: a device that comes up with Secure Boot already
		// enforcing (pre-enrolled hardware) skips the firmware step.
		return to == SBPending || to == SBEnrolled || to == TPM2Enrolled || to == Done || to == Failed || to == Canceled
	case SBPending:
		return to == SBEnrolled || to == Failed || to == Canceled
	case SBEnrolled:
		return to == TPM2Enrolled || to == Done || to == Failed || to == Canceled
	case TPM2Enrolled:
		return to == Done || to == Failed || to == Canceled
	case Failed:
		return to == Pending || to == Canceled // retry or give up
	default: // Done, Canceled are terminal
		return false
	}
}

// Job is one imaging assignment: image the device at MAC, on station Station,
// as asset tag Tag onto hardware profile Hardware, then carry it through the
// provisioning ceremony. It is console-authoritative (an operator creates it),
// unlike the station-replaced discovered set, so a station report can never
// clobber it.
type Job struct {
	Station  string `json:"station"`
	MAC      string `json:"mac"`
	Tag      string `json:"tag"`
	Hardware string `json:"hardware"`
	// Rev is the overlay revision to install: the one the device's ring is
	// pinned to, so the machine is CONVERGED the moment it boots. Empty means
	// the station falls back to the overlay's main branch - what an older
	// console sends, and what a device outside any ring gets.
	//
	// The console decides this because the station cannot: it knows a tag, not
	// a group, so it cannot work out which ring a device belongs to. Defaulting
	// to main is what made every freshly imaged device start life AHEAD of its
	// own ring - the engine records each promotion as a commit on main, so main
	// is permanently at least one commit past the ring it just pinned - and
	// comin refuses a head that is not a descendant of what it runs. Such a
	// device could not settle onto its ring until the ring passed the revision
	// it was born with.
	//
	// A revision rather than a branch name on purpose: the station pins an
	// exact rev anyway, and a ring branch can be force-moved between the
	// console deciding and the station installing.
	Rev    string `json:"rev,omitempty"`
	Status Status `json:"status"`
	// Progress is the percent-complete of the current step (0..100), for the
	// live progress bar. It is advisory display state, reset per step.
	Progress int `json:"progress,omitempty"`
	// Step is a short human label for what the station is doing right now
	// within Status (e.g. "wiping disk", "copying nix-store", "sbctl enroll").
	Step string `json:"step,omitempty"`
	// Message carries failure detail on Failed, and the one-shot LUKS recovery
	// key on Installed (until a proper secret store takes it over). Empty on
	// other transitions.
	Message string `json:"message,omitempty"`
}

var (
	macRE = regexp.MustCompile(`^([0-9a-f]{2}:){5}[0-9a-f]{2}$`)
	tagRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)
)

// Validate rejects a malformed job before it is stored. The MAC must be
// canonical (lower-case, colon-separated); normalise with NormalizeMAC first.
func (j Job) Validate() error {
	if j.Station == "" {
		return fmt.Errorf("image job needs a station")
	}
	if !macRE.MatchString(j.MAC) {
		return fmt.Errorf("image job needs a canonical MAC, got %q", j.MAC)
	}
	if !tagRE.MatchString(j.Tag) {
		return fmt.Errorf("image job tag %q must be a lowercase slug", j.Tag)
	}
	if strings.TrimSpace(j.Hardware) == "" {
		return fmt.Errorf("image job needs a hardware profile")
	}
	if j.Status != "" && !j.Status.Valid() {
		return fmt.Errorf("image job has an invalid status %q", j.Status)
	}
	if j.Progress < 0 || j.Progress > 100 {
		return fmt.Errorf("image job progress %d out of range 0..100", j.Progress)
	}
	return nil
}

// NormalizeMAC lower-cases and trims a MAC so equal addresses compare equal.
func NormalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}
