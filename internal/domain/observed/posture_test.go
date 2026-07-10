package observed

import "testing"

func TestPostureValidation(t *testing.T) {
	for _, s := range []SBState{SBUnknown, SBOff, SBAudit, SBEnforcing} {
		if !s.Valid() {
			t.Errorf("SB %q rejected", s)
		}
	}
	if SBState("bogus").Valid() {
		t.Error("bogus SB accepted")
	}
	for _, s := range []TPM2State{TPM2Unknown, TPM2Absent, TPM2Present, TPM2Enrolled} {
		if !s.Valid() {
			t.Errorf("TPM2 %q rejected", s)
		}
	}
	if TPM2State("bogus").Valid() {
		t.Error("bogus TPM2 accepted")
	}
}

func TestNextPostureStep(t *testing.T) {
	cases := []struct {
		name       string
		sb         SBState
		tpm2       TPM2State
		wantSB, wt bool
		expect     PostureStep
	}{
		{"nothing wanted", SBOff, TPM2Absent, false, false, PostureComplete},
		{"sb off -> audit", SBOff, TPM2Present, true, true, StepEnableAudit},
		{"sb audit -> enforce", SBAudit, TPM2Present, true, true, StepEnforceSB},
		{"sb done, tpm2 present -> enroll", SBEnforcing, TPM2Present, true, true, StepEnrollTPM2},
		{"sb done, tpm2 enrolled -> complete", SBEnforcing, TPM2Enrolled, true, true, PostureComplete},
		{"tpm2 absent but wanted", SBEnforcing, TPM2Absent, true, true, StepNoTPM2},
		{"unknown sb waits", SBUnknown, TPM2Unknown, true, true, PostureComplete},
		{"sb not wanted, tpm2 present", SBOff, TPM2Present, false, true, StepEnrollTPM2},
		{"tpm2 not wanted, sb audit", SBAudit, TPM2Enrolled, true, false, StepEnforceSB},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NextPostureStep(tc.sb, tc.tpm2, tc.wantSB, tc.wt); got != tc.expect {
				t.Fatalf("step = %q, want %q", got, tc.expect)
			}
		})
	}
}

func TestCheckInValidatesPosture(t *testing.T) {
	ok := CheckIn{Tag: "lt-1", SB: SBEnforcing, TPM2: TPM2Enrolled}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	// Empty posture is valid (old agent).
	if err := (CheckIn{Tag: "lt-1"}).Validate(); err != nil {
		t.Fatal(err)
	}
	if err := (CheckIn{Tag: "lt-1", SB: "weird"}).Validate(); err == nil {
		t.Error("bad SB accepted")
	}
	if err := (CheckIn{Tag: "lt-1", TPM2: "weird"}).Validate(); err == nil {
		t.Error("bad TPM2 accepted")
	}
}
