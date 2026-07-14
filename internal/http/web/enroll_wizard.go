package web

import (
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// enroll_wizard.go: the batch provisioning wizard. One page that adapts to the
// live state of a station's in-flight jobs - the install, Secure Boot and TPM2
// phases, per-device progress, the manual firmware step for the current phase
// (with an operator reboot-to-BIOS control), and one-shot recovery secrets.
// It renders whatever state each device is in; the station/agent advance the
// status, so a device whose group needs no Secure Boot simply jumps from
// installed to done and never shows the firmware phase.

// wizardRow is one device's live provisioning state in the batch.
type wizardRow struct {
	Job       imaging.Job
	Phase     string // install|secureboot|tpm2|done|halted
	Reboot    bool   // an operator reboot control applies (installed onward)
	HasSecret bool   // a sealed recovery secret exists to reveal (owner reach)
	LUKS      string // one-shot LUKS key, only when no secret store keeps it
	Bios      biosStep
}

// biosStep is the manual firmware action for a phase, with a brand-specific
// entry hint. Empty Title means no manual step (fully automatic phase).
type biosStep struct {
	Title string
	Key   string   // firmware entry key, e.g. "F1" (Lenovo), "F2" (Intel NUC)
	Steps []string // ordered BIOS actions
}

// firmwareKey guesses the BIOS-entry key from the hardware profile name.
func firmwareKey(hardware string) string {
	h := strings.ToLower(hardware)
	switch {
	case strings.Contains(h, "lenovo") || strings.Contains(h, "thinkpad"):
		return "F1"
	case strings.Contains(h, "nuc") || strings.Contains(h, "intel"):
		return "F2"
	case strings.Contains(h, "hp") || strings.Contains(h, "elitebook"):
		return "F10"
	default:
		return ""
	}
}

// biosFor returns the manual firmware step for a job's current phase. Only the
// Secure Boot phase needs a human at the BIOS; install and TPM2 are automatic.
func biosFor(j imaging.Job) biosStep {
	if j.Status != imaging.SBPending {
		return biosStep{}
	}
	return biosStep{
		Title: "Enable Secure Boot in the firmware",
		Key:   firmwareKey(j.Hardware),
		Steps: []string{
			"Enter the BIOS (tap the entry key on boot)",
			"Secure Boot -> Setup Mode (clear the existing keys)",
			"Secure Boot -> Enabled, then Save & Exit",
			"The device signs and enrols the platform keys on reboot",
		},
	}
}

// enrollWizard renders the batch provisioning wizard for a station.
func (s *Server) enrollWizard(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Imaging == nil {
		http.Error(w, "imaging execution needs the database (postgres not configured)", http.StatusServiceUnavailable)
		return
	}
	station := r.PathValue("station")
	jobs, err := s.svc.Imaging.List(r.Context(), station)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	secretsEnabled := s.svc.DeviceSecrets.Enabled()
	rows := make([]wizardRow, 0, len(jobs))
	active := map[string]bool{} // which phases still have an unfinished device
	var needFirmware bool
	for _, j := range jobs {
		st := j.Status
		row := wizardRow{Job: j, Phase: st.Phase(), Bios: biosFor(j)}
		switch st {
		case imaging.Installed, imaging.SBPending, imaging.SBEnrolled, imaging.TPM2Enrolled:
			row.Reboot = true
		}
		// A sealed recovery secret is revealable (owner reach) once it exists.
		// With no secret store the station keeps the key in the message for a
		// one-shot copy (password-manager workflow); surface that instead.
		if secretsEnabled {
			if metas, err := s.svc.DeviceSecrets.List(r.Context(), j.Tag); err == nil && len(metas) > 0 {
				row.HasSecret = true
			}
		} else if key, found := strings.CutPrefix(j.Message, imaging.LUKSRecoveryPrefix); found {
			row.LUKS = key
		}
		if st == imaging.SBPending {
			needFirmware = true
		}
		if !st.Terminal() && st != imaging.Failed && st != imaging.Canceled {
			active[st.Phase()] = true
		}
		rows = append(rows, row)
	}

	data := map[string]any{
		"Title": "Provisioning", "Nav": "enroll",
		"Station":      station,
		"Rows":         rows,
		"CanEdit":      v.roleAt("org").Meets(identity.Editor),
		"CanReveal":    v.roleAt("org").Meets(identity.Owner),
		"NeedFirmware": needFirmware,
		// Stepper phase flags: a phase is "active" if any device is still in it.
		"PhaseInstall": active["install"],
		"PhaseSB":      active["secureboot"],
		"PhaseTPM2":    active["tpm2"],
	}
	s.render(w, "wizard", data, v)
}
