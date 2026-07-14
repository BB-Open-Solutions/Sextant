package observed

import "fmt"

// posture.go: the device's security posture as the agent observes it -
// Secure Boot and TPM2 LUKS state. Reported, never declared: the console
// renders the gap between this and the target (config-as-data), so an
// operator sees the real state, not a checkbox they ticked.

// SBState is the observed Secure Boot state.
type SBState string

const (
	// SBUnknown means not reported (old agent, or probe failed).
	SBUnknown SBState = ""
	// SBOff means Secure Boot disabled in firmware.
	SBOff SBState = "off"
	// SBAudit means keys created/enrolled (sbctl), firmware still permissive -
	// the step between install and enforcing.
	SBAudit SBState = "audit"
	// SBEnforcing means Secure Boot on, only signed boot chain accepted.
	SBEnforcing SBState = "enforcing"
)

// TPM2State is the observed TPM2 LUKS-unlock state.
type TPM2State string

const (
	// TPM2Unknown means not reported.
	TPM2Unknown TPM2State = ""
	// TPM2Absent means no usable TPM2 device present.
	TPM2Absent TPM2State = "absent"
	// TPM2Present means a TPM2 exists but LUKS is not bound to it yet.
	TPM2Present TPM2State = "present"
	// TPM2Enrolled means LUKS auto-unlock is bound to the TPM2 (PCR7).
	TPM2Enrolled TPM2State = "enrolled"
)

// Valid reports whether s is a known Secure Boot state.
func (s SBState) Valid() bool {
	switch s {
	case SBUnknown, SBOff, SBAudit, SBEnforcing:
		return true
	}
	return false
}

// Valid reports whether s is a known TPM2 state.
func (s TPM2State) Valid() bool {
	switch s {
	case TPM2Unknown, TPM2Absent, TPM2Present, TPM2Enrolled:
		return true
	}
	return false
}

// validatePosture is folded into CheckIn.Validate.
func validatePosture(sb SBState, tpm2 TPM2State) error {
	if !sb.Valid() {
		return fmt.Errorf("unknown secure-boot state %q", sb)
	}
	if !tpm2.Valid() {
		return fmt.Errorf("unknown tpm2 state %q", tpm2)
	}
	return nil
}

// PostureStep is the next physical action an operator must take to move a
// device toward its target posture. Ordered: Secure Boot must reach
// enforcing before TPM2 is enrolled, or the PCR7 binding measures the
// wrong boot state.
type PostureStep string

const (
	// PostureComplete means observed posture already meets the target.
	PostureComplete PostureStep = "complete"
	// StepEnableAudit means set dawo.secureboot.enable and deploy; the device
	// creates and enrolls Secure Boot keys (audit mode).
	StepEnableAudit PostureStep = "enable-audit"
	// StepEnforceSB means reboot to firmware and switch Secure Boot on.
	StepEnforceSB PostureStep = "enforce-secureboot"
	// StepEnrollTPM2 means run systemd-cryptenroll to bind LUKS to PCR7.
	StepEnrollTPM2 PostureStep = "enroll-tpm2"
	// StepNoTPM2 means the target wants TPM2 unlock but no TPM2 is present -
	// a hardware/firmware issue, not a next action.
	StepNoTPM2 PostureStep = "no-tpm2"
)

// NextPostureStep computes the single next action from observed state and
// the resolved target (wantSB, wantTPM2 from the config chain). Unknown
// observed state yields no step: wait for a check-in from a posture-aware
// agent rather than guess.
func NextPostureStep(sb SBState, tpm2 TPM2State, wantSB, wantTPM2 bool) PostureStep {
	// Secure Boot first.
	if wantSB {
		switch sb {
		case SBUnknown:
			return PostureComplete // no data yet; nothing to instruct
		case SBOff:
			return StepEnableAudit
		case SBAudit:
			return StepEnforceSB
		}
	}
	// TPM2 only once Secure Boot is where it needs to be (or not wanted).
	if wantTPM2 {
		switch tpm2 {
		case TPM2Unknown:
			return PostureComplete
		case TPM2Absent:
			return StepNoTPM2
		case TPM2Present:
			return StepEnrollTPM2
		}
	}
	return PostureComplete
}
