package web

import (
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// posture.go joins a device's observed security posture (Secure Boot,
// TPM2) with its resolved target (config-as-data) to render the
// enrollment wizard's next step (design 0001).

// The catalog keys that express the posture targets in the DAWO core.
const (
	keySecureBoot = "secureboot.enable"
	keyTPM2       = "diskUnlock.tpm2.enable"
)

// postureView is the template's view of one device's posture.
type postureView struct {
	SB       observed.SBState
	TPM2     observed.TPM2State
	WantSB   bool
	WantTPM2 bool
	Step     observed.PostureStep
	// Reported is false when the agent sent no posture (old agent); the
	// panel then shows "waiting for a posture-aware check-in".
	Reported bool
}

// postureView derives the wizard view from the fleet document and status.
func (s *Server) postureView(f *fleet.Fleet, tag string, st app.StatusView) postureView {
	resolved := f.ResolveValues(tag)
	want := func(key string) bool {
		v, ok := resolved[key]
		b, _ := v.(bool)
		return ok && b
	}
	wantSB, wantTPM2 := want(keySecureBoot), want(keyTPM2)
	return postureView{
		SB:       st.SB,
		TPM2:     st.TPM2,
		WantSB:   wantSB,
		WantTPM2: wantTPM2,
		Step:     observed.NextPostureStep(st.SB, st.TPM2, wantSB, wantTPM2),
		Reported: st.SB != observed.SBUnknown || st.TPM2 != observed.TPM2Unknown,
	}
}

// StepText renders the human instruction for a step; kept in Go (not the
// template) so it can be localized later through the catalog.
func stepText(step observed.PostureStep) string {
	switch step {
	case observed.PostureComplete:
		return "Security posture complete."
	case observed.StepEnableAudit:
		return "Enable Secure Boot for this device (set secureboot.enable and deploy); the device creates and enrolls its keys in audit mode."
	case observed.StepEnforceSB:
		return "Reboot into firmware and switch Secure Boot ON."
	case observed.StepEnrollTPM2:
		return "Bind LUKS to the TPM2: run systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 <luks-device>, then reboot."
	case observed.StepNoTPM2:
		return "TPM2 auto-unlock is targeted but no TPM2 is present - check firmware/hardware."
	}
	return ""
}

// StepText exposes stepText to templates.
func (v postureView) StepText() string { return stepText(v.Step) }

// Complete reports whether nothing is left to do.
func (v postureView) Complete() bool { return v.Step == observed.PostureComplete && v.Reported }

// Warn reports a step that needs attention rather than routine progress.
func (v postureView) Warn() bool { return v.Step == observed.StepNoTPM2 }
