package web

import "testing"

// The property this exists for, measured on hardware 2026-08-04 (e2e5): an
// activation failed after /etc had already been switched, so the device
// reported the revision it ATTEMPTED. That matched the ring target exactly,
// and the console called the machine on spec while directory login, endpoint
// security and secret delivery were all dead on it.
//
// A matching revision proves what a device meant to run. On spec has to mean
// it works.
func TestDegradedDeviceIsNeverOnSpec(t *testing.T) {
	const rev = "aaaa1111"

	healthy := judgeDevice(rev, rev, true, true, false, false)
	if !healthy.OnSpec {
		t.Fatal("a device on its target with nothing failing is on spec")
	}

	degraded := judgeDevice(rev, rev, true, true, false, true)
	if degraded.OnSpec {
		t.Fatal("a device with failed units must not be on spec, however well its revision matches")
	}
	// The core question is separate and unaffected: a failed unit does not
	// mean the wrong system was installed. The pair exists precisely so one
	// can be true while the other is false.
	if !degraded.UpToDate {
		t.Fatal("failed units say nothing about which core is installed")
	}
	// Not "applying": nothing is closing this gap, and telling an operator to
	// wait is worse than telling them nothing.
	if degraded.Moving {
		t.Fatal("a degraded device is stuck, not moving")
	}
}

// The single-word chip has to agree with the pair, or the list and the device
// page contradict each other.
func TestDegradedDeviceDoesNotReadAsCurrent(t *testing.T) {
	const rev = "bbbb2222"
	if got := deviceConfigState(rev, rev, true, true, false, false); got != configCurrent {
		t.Fatalf("healthy device chip = %q, want %q", got, configCurrent)
	}
	if got := deviceConfigState(rev, rev, true, true, false, true); got == configCurrent {
		t.Fatal("a degraded device must not read as current")
	}
}

// Silence is not health. A device that never reported health (an older agent,
// or a probe that could not run) must be judged on its other signals rather
// than accused on a measurement it never made.
func TestUnreportedHealthIsNotAnAccusation(t *testing.T) {
	const rev = "cccc3333"
	v := judgeDevice(rev, rev, true, true, false, false)
	if !v.OnSpec {
		t.Fatal("a device that reported no health must not be treated as degraded")
	}
}
