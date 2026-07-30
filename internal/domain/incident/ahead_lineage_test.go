package incident

import (
	"strings"
	"testing"
	"time"
)

// A freshly imaged device is ALWAYS ahead of its ring, structurally: imaging
// installs from main, and the engine records each promotion as a commit on
// main, so a new device sits at least one commit past the ring it is joining.
// Calling that "out-of-band" trains an operator to ignore the one message that
// should mean somebody built a generation by hand.

func aheadObs(deployedRel, targetRel, headRel int) Observation {
	return Observation{
		Tag: "lt-new", Group: "laptops",
		Deployed: "dddddddddddd", Target: "tttttttttttt",
		DeployedRelease: deployedRel, TargetRelease: targetRel,
		Head: "hhhhhhhhhhhh", HeadRelease: headRel,
		Online: true, LastSeen: time.Now(),
	}
}

func TestAheadOnOurOwnLineageIsNotAFault(t *testing.T) {
	// Deployed 12, ring pinned at 11, repo tip 12: the imaging case.
	got := Detect([]Observation{aheadObs(12, 11, 12)}, time.Now())

	inc, ok := find(t, got, Behind)
	if !ok {
		t.Fatal("a device ahead of its ring reported nothing at all")
	}
	if inc.Severity != Info {
		t.Fatalf("severity %v; a device on this fleet's own newer configuration is waiting, not broken", inc.Severity)
	}
	if strings.Contains(inc.Action, "out-of-band") {
		t.Errorf("still blames an out-of-band change: %q", inc.Action)
	}
	if !strings.Contains(inc.Title, "waiting") {
		t.Errorf("title does not say what is actually happening: %q", inc.Title)
	}
}

// Past the repo's own tip is a different animal: nothing in this fleet's
// history produced it, so somebody built it by hand or the device follows
// another source. That must keep its warning and its blunt advice.
func TestAheadBeyondTheRepoTipStaysAWarning(t *testing.T) {
	got := Detect([]Observation{aheadObs(99, 11, 12)}, time.Now())

	inc, ok := find(t, got, Behind)
	if !ok {
		t.Fatal("a device past the repo tip reported nothing")
	}
	if inc.Severity != Warning {
		t.Fatalf("severity %v; a revision this fleet never produced deserves a warning", inc.Severity)
	}
	if !strings.Contains(inc.Action, "out-of-band") {
		t.Errorf("no longer names the likely cause: %q", inc.Action)
	}
}

// Behind is untouched by any of this: it is the ordinary lag and stays a
// warning, because an update that has not landed is worth chasing.
func TestBehindKeepsItsWarning(t *testing.T) {
	got := Detect([]Observation{aheadObs(9, 11, 12)}, time.Now())

	inc, ok := find(t, got, Behind)
	if !ok {
		t.Fatal("a lagging device reported nothing")
	}
	if inc.Severity != Warning {
		t.Fatalf("severity %v; falling behind is a warning", inc.Severity)
	}
	if !strings.Contains(inc.Title, "behind") {
		t.Errorf("title lost the plot: %q", inc.Title)
	}
}
