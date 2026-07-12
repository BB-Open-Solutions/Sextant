// Package imaging is the pure domain of the imaging-execution plane: an
// operator dispatches an image job for a discovered device, an imaging
// station polls its jobs and runs the install (nixos-anywhere), and reports
// progress back. It is intent-as-data, like device intents (ADR/design
// 0004): Sextant never drives the installer directly - it records what
// should happen and the station acts, so every image is an audited record
// and there is no live command channel into the imaging LAN.
package imaging

import (
	"fmt"
	"regexp"
	"strings"
)

// Status is where an image job is in its lifecycle.
type Status string

const (
	// Pending: dispatched by an operator, the station has not started yet.
	Pending Status = "pending"
	// Imaging: the station is running the install (nixos-anywhere).
	Imaging Status = "imaging"
	// Installed: the image is on disk; the device converges from here.
	Installed Status = "installed"
	// Failed: the install failed; Message carries the reason. Retryable.
	Failed Status = "failed"
	// Canceled: the operator withdrew the job before it completed.
	Canceled Status = "canceled"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case Pending, Imaging, Installed, Failed, Canceled:
		return true
	}
	return false
}

// Terminal reports whether no further transition is expected.
func (s Status) Terminal() bool { return s == Installed || s == Canceled }

// CanTransition reports whether a job may move from s to to. The station
// drives pending->imaging->installed and may fail from either active state;
// an operator may cancel a job that has not finished, and may retry a failed
// job back to pending. Nothing leaves a terminal state.
func (s Status) CanTransition(to Status) bool {
	if !to.Valid() {
		return false
	}
	switch s {
	case Pending:
		return to == Imaging || to == Failed || to == Canceled
	case Imaging:
		return to == Installed || to == Failed || to == Canceled
	case Failed:
		return to == Pending || to == Canceled // retry or give up
	default: // Installed, Canceled are terminal
		return false
	}
}

// Job is one imaging assignment: image the device at MAC, on station Station,
// as asset tag Tag onto hardware profile Hardware. It is console-authoritative
// (an operator creates it), unlike the station-replaced discovered set, so a
// station report can never clobber it.
type Job struct {
	Station  string `json:"station"`
	MAC      string `json:"mac"`
	Tag      string `json:"tag"`
	Hardware string `json:"hardware"`
	Status   Status `json:"status"`
	// Message carries failure detail (empty otherwise); operator-facing.
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
	return nil
}

// NormalizeMAC lower-cases and trims a MAC so equal addresses compare equal.
func NormalizeMAC(mac string) string {
	return strings.ToLower(strings.TrimSpace(mac))
}
