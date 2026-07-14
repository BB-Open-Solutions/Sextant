package imaging

import "testing"

func TestStatusTransitions(t *testing.T) {
	ok := [][2]Status{
		{Pending, Imaging}, {Pending, Failed}, {Pending, Canceled},
		{Imaging, Installed}, {Imaging, Failed}, {Imaging, Canceled},
		// From installed the job branches by resolved policy.
		{Installed, SBPending}, {Installed, TPM2Enrolled}, {Installed, Done}, {Installed, Failed},
		// Secure Boot ceremony, then TPM2 sealing, then done.
		{SBPending, SBEnrolled}, {SBPending, Failed},
		{SBEnrolled, TPM2Enrolled}, {SBEnrolled, Done}, {SBEnrolled, Failed},
		{TPM2Enrolled, Done}, {TPM2Enrolled, Failed},
		{Failed, Pending}, {Failed, Canceled},
	}
	for _, tc := range ok {
		if !tc[0].CanTransition(tc[1]) {
			t.Errorf("%s->%s should be allowed", tc[0], tc[1])
		}
	}
	bad := [][2]Status{
		{Installed, Imaging}, {Canceled, Pending}, {Done, Pending}, {Done, Failed},
		{Pending, Installed}, {Imaging, Pending}, {Pending, Status("bogus")},
		{SBEnrolled, SBPending}, {TPM2Enrolled, SBPending},
	}
	for _, tc := range bad {
		if tc[0].CanTransition(tc[1]) {
			t.Errorf("%s->%s should be rejected", tc[0], tc[1])
		}
	}
}

func TestStatusTerminalAndValid(t *testing.T) {
	if !Done.Terminal() || !Canceled.Terminal() {
		t.Fatal("done/canceled must be terminal")
	}
	for _, s := range []Status{Pending, Imaging, Installed, SBPending, SBEnrolled, TPM2Enrolled, Failed} {
		if s.Terminal() {
			t.Fatalf("non-terminal state %s marked terminal", s)
		}
	}
	for _, s := range []Status{Pending, Imaging, Installed, SBPending, SBEnrolled, TPM2Enrolled, Done, Failed, Canceled} {
		if !s.Valid() {
			t.Fatalf("known status %s reported invalid", s)
		}
	}
	if Status("bogus").Valid() {
		t.Fatal("bogus status reported valid")
	}
}

func TestStatusPhase(t *testing.T) {
	cases := map[Status]string{
		Pending: "install", Imaging: "install", Installed: "install",
		SBPending: "secureboot", SBEnrolled: "secureboot",
		TPM2Enrolled: "tpm2", Done: "done", Failed: "halted", Canceled: "halted",
	}
	for s, want := range cases {
		if got := s.Phase(); got != want {
			t.Errorf("%s.Phase() = %q, want %q", s, got, want)
		}
	}
}

func TestJobValidate(t *testing.T) {
	good := Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "lenovo-t495s", Status: Pending, Progress: 42}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	for name, j := range map[string]Job{
		"no station":        {MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw"},
		"bad mac":           {Station: "s", MAC: "AA-BB", Tag: "lab-1", Hardware: "hw"},
		"bad tag":           {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "Lab_1", Hardware: "hw"},
		"no hardware":       {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1"},
		"bad status":        {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw", Status: Status("x")},
		"progress too high": {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw", Progress: 101},
		"progress negative": {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw", Progress: -1},
	} {
		if err := j.Validate(); err == nil {
			t.Errorf("%s: expected rejection", name)
		}
	}
}

func TestNormalizeMAC(t *testing.T) {
	if NormalizeMAC("  AA:BB:CC:DD:EE:FF ") != "aa:bb:cc:dd:ee:ff" {
		t.Fatal("normalise wrong")
	}
}
