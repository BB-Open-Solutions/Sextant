package incident

import (
	"strings"
	"testing"
	"time"
)

// Only the CORE carries a version. A configuration is on spec or it is not,
// and an operator is never asked to reason about "release 274 versus 275" -
// that number describes the config plane, means nothing outside it, and
// invites exactly the question the version model exists to retire (#21).
//
// This is asserted rather than left to review because the wording had already
// been fixed on the device page and the updates board, and survived here: the
// incident detail is generated in the domain, so nothing in the web layer's
// vocabulary work touched it. It surfaced on hardware during e2e5.
func TestBehindDetailNamesSpecNotAReleaseNumber(t *testing.T) {
	now := time.Now()
	obs := Observation{
		Tag: "lt-1", Group: "laptops",
		Deployed: "aaaaaaaaaaaa", Target: "bbbbbbbbbbbb",
		DeployedRelease: 274, TargetRelease: 275,
		Head: "bbbbbbbbbbbb", HeadRelease: 275,
		Online: true, LastSeen: now,
	}

	inc, ok := find(t, Detect([]Observation{obs}, now), Behind)
	if !ok {
		t.Fatal("a device behind its target must raise an incident")
	}

	if strings.Contains(strings.ToLower(inc.Detail), "release") {
		t.Fatalf("detail must not name a release number, got: %q", inc.Detail)
	}
	if !strings.Contains(inc.Detail, "spec") {
		t.Fatalf("detail should say the device is not on spec, got: %q", inc.Detail)
	}
	// The revisions stay reachable for whoever asks - they are forensics, not
	// the headline.
	if !strings.Contains(inc.Detail, "aaaaaaaa") || !strings.Contains(inc.Detail, "bbbbbbbb") {
		t.Fatalf("detail should still carry both revisions, got: %q", inc.Detail)
	}
}
