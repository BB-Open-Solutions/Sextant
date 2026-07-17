package web

import (
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// enroll_wizard.go: the batch provisioning wizard. One page that adapts to the
// live state of a station's in-flight jobs - the install, Secure Boot and TPM2
// phases, per-device progress, the manual firmware step for the current phase
// (with an operator reboot control), and one-shot recovery secrets.
//
// The page instructs, the device advances: every transition is derived from
// what the device itself reports on check-in (posture, executor acks - see
// imaging.Advance), so a step only turns green when it verifiably happened.
// Steps a device cannot do are skipped by the same rule (no EFI, no TPM2 chip,
// no TPM2 unlock configured), and each row carries a plain-language "what is
// happening / what to do now" line so a non-expert can run the whole ceremony.

// wizardRow is one device's live provisioning state in the batch.
type wizardRow struct {
	Job       imaging.Job
	Phase     string // install|secureboot|tpm2|done|halted
	Reboot    bool   // an operator reboot control applies (installed onward)
	HasSecret bool   // a sealed recovery secret exists to reveal (owner reach)
	LUKS      string // one-shot LUKS key, only when no secret store keeps it
	Bios      biosStep
	// NowKey is the catalog key of the guidance line for the row's current
	// state. Empty on done/halted (those render their own card).
	NowKey string
	// Observed posture (empty until the device's first check-in) and whether
	// the device is currently checking in - drives the verification chips.
	SB     observed.SBState
	TPM2   observed.TPM2State
	Online bool
}

// biosStep is the manual firmware action for a phase, with a brand-specific
// entry hint. Empty TitleKey means no manual step (fully automatic phase).
// The fields are catalog keys, translated at render.
type biosStep struct {
	TitleKey string
	Key      string   // firmware entry key, e.g. "F1" (Lenovo), "F2" (Intel NUC)
	StepKeys []string // ordered BIOS actions
}

// firmwareKey guesses the BIOS-entry key from the hardware profile name.
func firmwareKey(hardware string) string {
	h := strings.ToLower(hardware)
	switch {
	case strings.Contains(h, "lenovo") || strings.Contains(h, "thinkpad") || strings.Contains(h, "t495"):
		return "F1"
	case strings.Contains(h, "nuc") || strings.Contains(h, "intel") || strings.Contains(h, "msi"):
		return "F2"
	case strings.Contains(h, "hp") || strings.Contains(h, "elitebook"):
		return "F10"
	default:
		return ""
	}
}

// biosFor returns the manual firmware step for a job's current phase. Only the
// Secure Boot phase needs a human at the machine; everything else is automatic.
func biosFor(j imaging.Job) biosStep {
	if j.Status != imaging.SBPending {
		return biosStep{}
	}
	return biosStep{
		TitleKey: "wizard.bios_title",
		Key:      firmwareKey(j.Hardware),
		StepKeys: []string{
			"wizard.bios_s1", // power off (or use the Reboot button) and tap the entry key
			"wizard.bios_s2", // Security -> Secure Boot -> Enabled
			"wizard.bios_s3", // Reset to Setup Mode / Clear Secure Boot keys
			"wizard.bios_s4", // Save & Exit and let it boot
			"wizard.bios_s5", // the rest is automatic (enrol + reboots)
		},
	}
}

// nowKeyFor picks the plain-language guidance line for a row.
func nowKeyFor(st imaging.Status) string {
	switch st {
	case imaging.Pending:
		return "wizard.now_pending"
	case imaging.Imaging:
		return "wizard.now_imaging"
	case imaging.Installed:
		return "wizard.now_installed"
	case imaging.SBPending:
		return "wizard.now_sb_pending"
	case imaging.SBEnrolled:
		return "wizard.now_sb_enrolled"
	case imaging.TPM2Enrolled:
		return "wizard.now_tpm2"
	}
	return ""
}

// enrollWizard renders the batch provisioning wizard for a station.
func (s *Server) enrollWizard(w http.ResponseWriter, r *http.Request, v view) {
	// Provisioning is an org-Editor action; without this gate any authenticated
	// low-privilege user could read another station's imaging state.
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	if s.svc.Imaging == nil {
		http.Error(w, "imaging execution needs the database (postgres not configured)", http.StatusServiceUnavailable)
		return
	}
	// A one-shot LUKS recovery key is break-glass material: only an org Owner may
	// see it (mirroring the sealed-store reveal gate). Lower roles provision but
	// never read the key.
	canSeeKey := v.roleAt("org").Meets(identity.Owner)
	station := r.PathValue("station")
	jobs, err := s.svc.Imaging.List(r.Context(), station)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	secretsEnabled := s.svc.DeviceSecrets.Enabled()
	rows := make([]wizardRow, 0, len(jobs))
	// The stepper shows cumulative progress: a phase lights up once ANY
	// device has reached or passed it, so a device that flies through a
	// phase between two polls still leaves its trail. (The old "phase has an
	// unfinished device" rule left later steps dark forever.)
	phaseRank := map[string]int{"install": 1, "secureboot": 2, "tpm2": 3, "done": 4}
	furthest := 0
	var needFirmware bool
	for _, j := range jobs {
		st := j.Status
		row := wizardRow{Job: j, Phase: st.Phase(), Bios: biosFor(j), NowKey: nowKeyFor(st)}
		switch st {
		case imaging.Installed, imaging.SBPending, imaging.SBEnrolled, imaging.TPM2Enrolled:
			row.Reboot = true
		}
		// Observed posture for the verification chips; absent until the
		// device's first check-in.
		if s.svc.Inventory != nil {
			if dst, ok, err := s.svc.Inventory.Status(r.Context(), j.Tag); err == nil && ok {
				row.SB, row.TPM2, row.Online = dst.SB, dst.TPM2, dst.Online
			}
		}
		// A sealed recovery secret is revealable (owner reach) once it exists.
		// With no secret store the station keeps the key in the message for a
		// one-shot copy (password-manager workflow); surface that instead.
		if secretsEnabled {
			if metas, err := s.svc.DeviceSecrets.List(r.Context(), j.Tag); err == nil && len(metas) > 0 {
				row.HasSecret = true
			}
		} else if key, found := strings.CutPrefix(j.Message, imaging.LUKSRecoveryPrefix); found && canSeeKey {
			row.LUKS = key
		}
		if st == imaging.SBPending {
			needFirmware = true
		}
		if r := phaseRank[st.Phase()]; r > furthest {
			furthest = r
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
		// Stepper phase flags: cumulative - lit once reached or passed.
		"PhaseInstall": furthest >= 1,
		"PhaseSB":      furthest >= 2,
		"PhaseTPM2":    furthest >= 3,
		"PhaseDone":    furthest >= 4,
	}
	s.render(w, "wizard", data, v)
}
