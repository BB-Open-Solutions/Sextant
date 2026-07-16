package imaging

import "code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"

// provision.go: the wizard's advancement rule. After install the ceremony is
// driven by what the DEVICE reports on check-in - the executor's acks for the
// milestones it carried out, and the observed posture for the states only the
// firmware can prove. The console never advances a job on a button press or a
// request it merely sent; every transition here traces to a device report, so
// the wizard's final "done" IS the verification Bram asked for: Secure Boot
// observed enforcing after the last reboot, TPM2 sealing acknowledged by the
// executor that performed it.

// NeedsProvisioning reports whether a job in this status should receive a
// provision intent on check-in: the device-side executor advances whatever
// ceremony step is possible (enrol platform keys in setup mode, seal the LUKS
// keyslot once Secure Boot enforces) and acks the milestone.
func (s Status) NeedsProvisioning() bool {
	switch s {
	case Installed, SBPending, SBEnrolled, TPM2Enrolled:
		return true
	}
	return false
}

// Advance decides the next status for a provisioning job from what the device
// just reported. ok is false when nothing should change this beat (waiting on
// a firmware step, a reboot, or the executor). Acks are considered before
// posture: they carry the executor's own outcome, including failures.
//
// Capability skips (a device that cannot do a step never shows it):
//   - No EFI at all (the posture-aware agent reports no Secure Boot state but
//     does report TPM2): Secure Boot is impossible and a TPM2 seal without it
//     binds to a meaningless PCR 7 - the job completes at install.
//   - No TPM2 chip ("absent"), or a config that wires no TPM2 unlock (the
//     agent reports "present" only when /etc/crypttab lacks a tpm2-device
//     entry): the ceremony ends after Secure Boot.
func Advance(current Status, sb observed.SBState, tpm2 observed.TPM2State, ack string) (to Status, message string, ok bool) {
	switch ack {
	case observed.AckSBEnrollFailed:
		return Failed, "Secure Boot key enrolment failed on the device", current != Failed
	case observed.AckTPM2EnrollFailed:
		return Failed, "TPM2 enrolment failed on the device", current != Failed
	case observed.AckTPM2Enrolled:
		if current.CanTransition(TPM2Enrolled) {
			return TPM2Enrolled, "", true
		}
	case observed.AckSBEnrolled:
		if current.CanTransition(SBEnrolled) {
			return SBEnrolled, "", true
		}
	}

	switch current {
	case Installed:
		// The first posture beat after the install decides the path.
		switch {
		case sb == observed.SBEnforcing:
			return SBEnrolled, "", true
		case sb == observed.SBOff || sb == observed.SBAudit:
			return SBPending, "", true
		case sb == observed.SBUnknown && tpm2 != observed.TPM2Unknown:
			return Done, "", true // non-EFI machine: skip the whole ceremony
		}
	case SBPending:
		// The operator flipped the firmware and the executor enrolled the
		// keys; enforcing is the firmware's own word for it.
		if sb == observed.SBEnforcing {
			return SBEnrolled, "", true
		}
	case SBEnrolled:
		switch tpm2 {
		case observed.TPM2Absent, observed.TPM2Present:
			return Done, "", true // no chip, or no TPM2 unlock configured
		}
	case TPM2Enrolled:
		// The device rebooted after sealing and still boots enforcing: the
		// ceremony held end-to-end.
		if sb == observed.SBEnforcing {
			return Done, "", true
		}
	}
	return current, "", false
}
