package imaging

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func TestNeedsProvisioning(t *testing.T) {
	for _, st := range []Status{Installed, SBPending, SBEnrolled, TPM2Enrolled} {
		if !st.NeedsProvisioning() {
			t.Errorf("%s should need provisioning", st)
		}
	}
	for _, st := range []Status{Pending, Imaging, Done, Failed, Canceled} {
		if st.NeedsProvisioning() {
			t.Errorf("%s should not need provisioning", st)
		}
	}
}

func TestAdvanceCeremony(t *testing.T) {
	cases := []struct {
		name     string
		from     Status
		sb       observed.SBState
		tpm2     observed.TPM2State
		ack      string
		wantSB   bool
		wantTPM2 bool
		want     Status
		wantOK   bool
		wantMsg  bool
	}{
		// The happy path, beat by beat: first boot with staged keys ->
		// firmware step -> executor enrols -> executor seals -> final boot.
		{"first boot, keys staged", Installed, observed.SBAudit, observed.TPM2Enrolled, "", true, true, SBPending, true, false},
		{"waiting on the firmware toggle", SBPending, observed.SBAudit, observed.TPM2Enrolled, "", true, true, SBPending, false, false},
		{"executor enrolled the keys", SBPending, observed.SBAudit, observed.TPM2Enrolled, observed.AckSBEnrolled, true, true, SBEnrolled, true, false},
		{"post-reboot, firmware enforces", SBPending, observed.SBEnforcing, observed.TPM2Enrolled, "", true, true, SBEnrolled, true, false},
		{"executor sealed the TPM2", SBEnrolled, observed.SBEnforcing, observed.TPM2Enrolled, observed.AckTPM2Enrolled, true, true, TPM2Enrolled, true, false},
		{"final boot still enforcing", TPM2Enrolled, observed.SBEnforcing, observed.TPM2Enrolled, "", true, true, Done, true, false},
		{"final boot posture unknown", TPM2Enrolled, observed.SBUnknown, observed.TPM2Unknown, "", true, true, TPM2Enrolled, false, false},

		// Capability skips.
		{"non-EFI machine skips it all", Installed, observed.SBUnknown, observed.TPM2Absent, "", true, true, Done, true, false},
		{"old agent, no posture: wait", Installed, observed.SBUnknown, observed.TPM2Unknown, "", true, true, Installed, false, false},
		{"no TPM2 chip after SB", SBEnrolled, observed.SBEnforcing, observed.TPM2Absent, "", true, true, Done, true, false},
		{"chip present, wanted: wait for the executor", SBEnrolled, observed.SBEnforcing, observed.TPM2Present, "", true, true, SBEnrolled, false, false},
		{"chip present, not wanted", SBEnrolled, observed.SBEnforcing, observed.TPM2Present, "", true, false, Done, true, false},
		{"pre-enrolled hardware", Installed, observed.SBEnforcing, observed.TPM2Enrolled, "", true, true, SBEnrolled, true, false},

		// Executor failures halt the job with a reason.
		{"sb enrol failed", SBPending, observed.SBAudit, observed.TPM2Enrolled, observed.AckSBEnrollFailed, true, true, Failed, true, true},
		{"tpm2 enrol failed", SBEnrolled, observed.SBEnforcing, observed.TPM2Enrolled, observed.AckTPM2EnrollFailed, true, true, Failed, true, true},

		// Config gates: no Secure Boot targeted -> the first sign of life
		// completes the job; no TPM2 targeted -> done after Secure Boot.
		{"config wants no SB", Installed, observed.SBAudit, observed.TPM2Present, "", false, false, Done, true, false},
		{"config wants no SB, no posture yet", Installed, observed.SBUnknown, observed.TPM2Unknown, "", false, false, Installed, false, false},
		{"config wants no TPM2 after SB", SBEnrolled, observed.SBEnforcing, observed.TPM2Enrolled, "", true, false, Done, true, false},

		// States outside the ceremony never move.
		{"imaging is the station's plane", Imaging, observed.SBEnforcing, observed.TPM2Enrolled, "", true, true, Imaging, false, false},
		{"done is terminal", Done, observed.SBEnforcing, observed.TPM2Enrolled, "", true, true, Done, false, false},
	}
	for _, c := range cases {
		to, msg, ok := Advance(c.from, c.sb, c.tpm2, c.ack, c.wantSB, c.wantTPM2)
		if ok != c.wantOK || (ok && to != c.want) {
			t.Errorf("%s: Advance(%s) = %s,%v want %s,%v", c.name, c.from, to, ok, c.want, c.wantOK)
		}
		if c.wantMsg && msg == "" {
			t.Errorf("%s: expected a failure message", c.name)
		}
		if ok && !c.from.CanTransition(to) {
			t.Errorf("%s: Advance proposes %s->%s which CanTransition forbids", c.name, c.from, to)
		}
	}
}
