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
// wantSB/wantTPM2 are the device's RESOLVED config targets (secureboot.enable,
// diskUnlock.tpm2.enable). They gate the whole ceremony: a device whose config
// does not enable Secure Boot runs an UNSIGNED bootloader - walking an
// operator through the firmware toggle there would leave the machine unable
// to boot. Config says which steps apply; the device reports when they
// happened. TPM2 sealing requires Secure Boot (PCR 7 measures it), so without
// wantSB the job completes at install either way.
//
// Capability skips (a device that cannot do a step never shows it):
//   - No EFI at all (the posture-aware agent reports no Secure Boot state but
//     does report TPM2): Secure Boot is impossible - the job completes at
//     install even when the config asks for it.
//   - No TPM2 chip ("absent"), or a config that does not want the TPM2
//     unlock (wantTPM2 false): the ceremony ends after Secure Boot.
func Advance(current Status, sb observed.SBState, tpm2 observed.TPM2State, ack string, wantSB, wantTPM2 bool) (to Status, message string, ok bool) {
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
		case !wantSB:
			// Config targets no Secure Boot: the ceremony does not apply
			// (and TPM2 without it would bind a meaningless PCR 7). The
			// first check-in - proof the install boots - completes the job.
			if sb != observed.SBUnknown || tpm2 != observed.TPM2Unknown {
				return Done, "", true
			}
		case sb == observed.SBEnforcing:
			return SBEnrolled, "", true
		case sb == observed.SBOff || sb == observed.SBAudit:
			return SBPending, "", true
		case sb == observed.SBUnknown && tpm2 != observed.TPM2Unknown:
			return Done, "", true // non-EFI machine: cannot do Secure Boot
		}
	case SBPending:
		// The operator flipped the firmware and the executor enrolled the
		// keys; enforcing is the firmware's own word for it.
		if sb == observed.SBEnforcing {
			return SBEnrolled, "", true
		}
	case SBEnrolled:
		if !wantTPM2 {
			return Done, "", true
		}
		if tpm2 == observed.TPM2Absent {
			return Done, "", true // wanted, but no chip: skip with reason
		}
		// Chip present and the config wants the unlock: wait for the
		// executor's tpm2-enrolled ack. ("Present" is NOT read as "not
		// configured" - the agent cannot see the initrd's crypttab, so
		// the resolved config is the only wish that counts.)
	case TPM2Enrolled:
		// The device rebooted after sealing and still boots enforcing: the
		// ceremony held end-to-end.
		if sb == observed.SBEnforcing {
			return Done, "", true
		}
	}
	return current, "", false
}
