package web

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// posture.go joins a device's observed security posture (Secure Boot,
// TPM2) with its resolved target (config-as-data) to render the
// enrollment wizard's next step (design 0001).

// The catalog keys that express the posture targets in the DAWO core live
// in the app layer (app.KeySecureBoot, app.KeyTPM2) so the baseline verdict
// (design 0008) and this wizard judge the same targets.
const (
	keySecureBoot = app.KeySecureBoot
	keyTPM2       = app.KeyTPM2
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
//
// The two config-driven steps say when they land: Secure Boot key enrollment
// and TPM2 binding happen while a device is imaged (design 0001, decision
// 2026-07-28), so for a device that is already enrolled the instruction is
// staged work, not a live ceremony. Saying so beats an operator waiting for a
// green chip that cannot arrive.
func stepText(step observed.PostureStep) string {
	switch step {
	case observed.PostureComplete:
		return "Security posture complete."
	case observed.StepEnableAudit:
		return "Enable Secure Boot for this device (set secureboot.enable and deploy); the device creates and enrolls its keys in audit mode (applies at the next re-image for an already-enrolled device)."
	case observed.StepEnforceSB:
		return "Reboot into firmware and switch Secure Boot ON."
	case observed.StepEnrollTPM2:
		return "Bind LUKS to the TPM2: run systemd-cryptenroll --tpm2-device=auto --tpm2-pcrs=7 <luks-device>, then reboot (applies at the next re-image for an already-enrolled device)."
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

// postureActions are the config-writable buttons the panel offers per
// device. Physical steps (reboot to firmware) have no action - the panel
// only shows their instruction.
type postureAction struct {
	Action string // form value
	Label  string
	Quiet  bool // secondary styling (the temporary-off path)
}

// Actions lists the buttons for the current posture. Enabling Secure Boot
// puts the device into audit mode on the next converge; disabling it is
// the temporary step before a reinstall (imaging needs SB off). TPM2 gets
// a config toggle; the actual key enrolment stays a one-time on-device
// command shown in the step text.
func (v postureView) Actions() []postureAction {
	var out []postureAction
	switch v.SB {
	case observed.SBOff:
		out = append(out, postureAction{"enable-sb", "Enable Secure Boot", false})
	case observed.SBEnforcing, observed.SBAudit:
		// Temporary-off for reinstall; deliberately secondary.
		out = append(out, postureAction{"disable-sb", "Disable Secure Boot (for reinstall)", true})
	}
	if v.WantTPM2 && v.TPM2 == observed.TPM2Present {
		out = append(out, postureAction{"enable-tpm2", "Target TPM2 unlock", false})
	}
	return out
}

// postDevicePosture applies one posture config action at the device scope
// (Editor). It only ever writes the two documented posture keys - never an
// arbitrary key - so the button surface cannot become a generic writer.
// Every write rides the gate and lands as an audited commit; the overlay
// for this one laptop regenerates with the new boot config.
func (s *Server) postDevicePosture(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	ref := "device:" + tag
	if err := s.requireWeb(v, ref, identity.Editor); err != nil {
		return err
	}
	var key string
	var val any
	var what string
	switch r.FormValue("action") {
	case "enable-sb":
		key, val, what = keySecureBoot, true, "enable secure boot"
	case "disable-sb":
		key, val, what = keySecureBoot, false, "disable secure boot (reinstall)"
	case "enable-tpm2":
		key, val, what = keyTPM2, true, "target tpm2 unlock"
	default:
		return fmt.Errorf("unknown posture action")
	}
	// The disable path is exactly the brick the guard exists for: firmware
	// first, config second (see app.GuardBrickingSettings).
	if b, isBool := val.(bool); isBool && key == keySecureBoot && !b {
		if err := app.GuardBrickingSettings(r.Context(), s.svc.Config, s.svc.Inventory, ref,
			[]app.SettingChange{{Key: key, RawValue: "false"}}); err != nil {
			return err
		}
	}
	msg := fmt.Sprintf("posture: %s at %s", what, ref)
	if err := s.applyGated(r, v, fleet.SetScopeSetting(ref, key, val),
		msg, app.AffectedHosts(s.svc.Config.Fleet(), ref)...); err != nil {
		return err
	}
	http.Redirect(w, r, "/devices/"+tag, http.StatusSeeOther)
	return nil
}
