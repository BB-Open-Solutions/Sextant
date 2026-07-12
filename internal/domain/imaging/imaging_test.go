package imaging

import "testing"

func TestStatusTransitions(t *testing.T) {
	ok := [][2]Status{
		{Pending, Imaging}, {Pending, Failed}, {Pending, Canceled},
		{Imaging, Installed}, {Imaging, Failed}, {Imaging, Canceled},
		{Failed, Pending}, {Failed, Canceled},
	}
	for _, tc := range ok {
		if !tc[0].CanTransition(tc[1]) {
			t.Errorf("%s->%s should be allowed", tc[0], tc[1])
		}
	}
	bad := [][2]Status{
		{Installed, Imaging}, {Installed, Failed}, {Canceled, Pending},
		{Pending, Installed}, {Imaging, Pending}, {Pending, Status("bogus")},
	}
	for _, tc := range bad {
		if tc[0].CanTransition(tc[1]) {
			t.Errorf("%s->%s should be rejected", tc[0], tc[1])
		}
	}
}

func TestStatusTerminalAndValid(t *testing.T) {
	if !Installed.Terminal() || !Canceled.Terminal() {
		t.Fatal("installed/canceled must be terminal")
	}
	if Pending.Terminal() || Imaging.Terminal() || Failed.Terminal() {
		t.Fatal("non-terminal states marked terminal")
	}
	if Status("bogus").Valid() {
		t.Fatal("bogus status reported valid")
	}
}

func TestJobValidate(t *testing.T) {
	good := Job{Station: "nuc-1", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "lenovo-t495s", Status: Pending}
	if err := good.Validate(); err != nil {
		t.Fatalf("valid job rejected: %v", err)
	}
	for name, j := range map[string]Job{
		"no station":  {MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw"},
		"bad mac":     {Station: "s", MAC: "AA-BB", Tag: "lab-1", Hardware: "hw"},
		"bad tag":     {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "Lab_1", Hardware: "hw"},
		"no hardware": {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1"},
		"bad status":  {Station: "s", MAC: "aa:bb:cc:dd:ee:ff", Tag: "lab-1", Hardware: "hw", Status: Status("x")},
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
