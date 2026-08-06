package discovery

import (
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func validDiscovery() Discovered {
	return Discovered{MAC: "aa:bb:cc:dd:ee:ff", Phase: observed.Discovered, LastSeen: time.Unix(1, 0)}
}

func TestDiscoveredValidateAcceptsCanonical(t *testing.T) {
	if err := validDiscovery().Validate(); err != nil {
		t.Fatalf("valid discovery rejected: %v", err)
	}
}

func TestDiscoveredValidateRejectsBadMAC(t *testing.T) {
	for _, mac := range []string{"", "not-a-mac", "AA:BB:CC:DD:EE:FF", "aa:bb:cc:dd:ee", "aabbccddeeff"} {
		d := validDiscovery()
		d.MAC = mac
		if err := d.Validate(); err == nil {
			t.Errorf("MAC %q accepted, want reject", mac)
		}
	}
}

func TestDiscoveredValidateRejectsPostEnrollmentPhase(t *testing.T) {
	d := validDiscovery()
	d.Phase = observed.Running
	if err := d.Validate(); err == nil {
		t.Fatal("Running phase accepted before enrollment, want reject")
	}
	d.Phase = ""
	if err := d.Validate(); err == nil {
		t.Fatal("empty phase accepted, want reject")
	}
}

func TestDiscoveredValidateBoundsFields(t *testing.T) {
	d := validDiscovery()
	d.Vendor = strings.Repeat("x", maxStringField+1)
	if err := d.Validate(); err == nil {
		t.Fatal("over-long vendor accepted")
	}
	d = validDiscovery()
	d.Facter = strings.Repeat("y", maxFacterBytes+1)
	if err := d.Validate(); err == nil {
		t.Fatal("over-large facter accepted")
	}
	d = validDiscovery()
	d.Cores = -1
	if err := d.Validate(); err == nil {
		t.Fatal("negative core count accepted")
	}
}

func TestNormalizeMAC(t *testing.T) {
	if got := NormalizeMAC("  AA:BB:CC:DD:EE:FF "); got != "aa:bb:cc:dd:ee:ff" {
		t.Fatalf("NormalizeMAC = %q", got)
	}
}

func TestReportValidateNormalizesAndDedupes(t *testing.T) {
	r := Report{Devices: []Discovered{
		{MAC: "AA:BB:CC:DD:EE:01", Phase: observed.Discovered},
		{MAC: "aa:bb:cc:dd:ee:02", Phase: observed.Installing},
	}}
	if err := r.Validate(); err != nil {
		t.Fatalf("valid report rejected: %v", err)
	}
	if r.Devices[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Fatalf("report did not normalise MAC: %q", r.Devices[0].MAC)
	}

	dup := Report{Devices: []Discovered{
		{MAC: "aa:bb:cc:dd:ee:03", Phase: observed.Discovered},
		{MAC: "AA:BB:CC:DD:EE:03", Phase: observed.Discovered},
	}}
	if err := dup.Validate(); err == nil {
		t.Fatal("duplicate MAC (after normalisation) accepted")
	}
}

func TestNormalizePhase(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want observed.Phase
	}{
		{"pxe", observed.Discovered},
		{"PXE", observed.Discovered},
		{" netboot ", observed.Discovered},
		{"discovered", observed.Discovered},
		{"installing", observed.Installing},
		{"bogus", observed.Phase("bogus")},
	} {
		if got := NormalizePhase(observed.Phase(tc.in)); got != tc.want {
			t.Fatalf("NormalizePhase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A real imaging station posts phase "pxe" off the DHCP lease; the report must
// be accepted (mapped to Discovered), not 400'd.
func TestReportAcceptsStationPXEPhase(t *testing.T) {
	r := Report{Devices: []Discovered{{MAC: "aa:bb:cc:dd:ee:0a", Phase: observed.Phase("pxe")}}}
	if err := r.Validate(); err != nil {
		t.Fatalf("station pxe report rejected: %v", err)
	}
	if r.Devices[0].Phase != observed.Discovered {
		t.Fatalf("pxe phase not normalised, got %q", r.Devices[0].Phase)
	}
}

func TestReportValidateBoundsBatch(t *testing.T) {
	big := Report{Devices: make([]Discovered, MaxBatch+1)}
	if err := big.Validate(); err == nil {
		t.Fatal("oversized batch accepted")
	}
}

// TestReportAcceptsAHyphenatedStation covers what NormalizeMAC's comment
// always claimed and did not do. A report is validated as one unit, and
// Validate demands the canonical colon form - so before 2026-08-07 a station
// that spelled MACs with hyphens (Windows, some dnsmasq lease dumps) had its
// WHOLE report rejected, not just the offending entry.
func TestReportAcceptsAHyphenatedStation(t *testing.T) {
	r := Report{Devices: []Discovered{
		{MAC: "AA-BB-CC-DD-EE-01", Phase: "pxe"},
		{MAC: "aa:bb:cc:dd:ee:02", Phase: "pxe"},
	}}
	if err := r.Validate(); err != nil {
		t.Fatalf("a hyphenated report was rejected: %v", err)
	}
	if r.Devices[0].MAC != "aa:bb:cc:dd:ee:01" {
		t.Errorf("MAC normalised to %q", r.Devices[0].MAC)
	}
	// And the duplicate check still works ACROSS spellings: the same address
	// twice in two formats is one device, and letting it through would give a
	// station two lease entries for one machine.
	dup := Report{Devices: []Discovered{
		{MAC: "AA-BB-CC-DD-EE-01", Phase: "pxe"},
		{MAC: "aa:bb:cc:dd:ee:01", Phase: "pxe"},
	}}
	if err := dup.Validate(); err == nil {
		t.Error("the same MAC in two spellings passed the duplicate check")
	}
}
