package main

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
)

// TestStationWalkIsATransitionTheConsoleAccepts ties the simulator's state
// machine to the domain's. A simulator that walks a path the console refuses
// would still look busy in the logs while every status call 400s, and the demo
// would show a job stuck on "imaging" with no reason on screen.
//
// This is the test that fails when somebody changes imaging.Status.
func TestStationWalkIsATransitionTheConsoleAccepts(t *testing.T) {
	for _, sb := range []bool{true, false} {
		s := newStationSim("http://x", "t", "sta", 1, 0, sb, newCredStore())
		cur := imaging.Imaging
		seen := 0
		for {
			nx := s.next(string(cur))
			if nx == "" {
				break
			}
			if !cur.CanTransition(imaging.Status(nx)) {
				t.Fatalf("secureBoot=%v: the console refuses %s -> %s", sb, cur, nx)
			}
			cur = imaging.Status(nx)
			seen++
			if seen > 10 {
				t.Fatalf("secureBoot=%v: the walk does not terminate", sb)
			}
		}
		if cur != imaging.Done {
			t.Errorf("secureBoot=%v: the walk ends at %s, not done", sb, cur)
		}
		// Secure Boot on has to be the longer road, or the flag decides
		// nothing and the demo shows the same thing either way.
		if sb && seen < 4 {
			t.Errorf("secure boot walk is %d steps; the ceremony is missing", seen)
		}
	}
}

// TestStationFailsFromAnActiveState: the failure path is demo material too (an
// install that breaks and is retried), so it has to be a transition the
// console accepts rather than a status it rejects.
func TestStationFailsFromAnActiveState(t *testing.T) {
	for _, from := range []imaging.Status{imaging.Imaging, imaging.Installed} {
		if !from.CanTransition(imaging.Failed) {
			t.Errorf("the console refuses %s -> failed, so the simulator must not report it", from)
		}
	}
}

// TestStationPoolStaysPut: a finished machine leaves the workbench and a fresh
// one arrives. Without that a demo runs out of things to enrol after three
// clicks, and with a leak it grows without bound on a long-running instance.
func TestStationPoolStaysPut(t *testing.T) {
	s := newStationSim("http://x", "t", "sta", 3, 0, false, newCredStore())
	if len(s.machines) != 3 {
		t.Fatalf("pool starts at %d, want 3", len(s.machines))
	}
	for mac := range s.machines {
		s.machines[mac].job = &jobRun{Status: "done", finished: true, failAt: -1}
		break
	}
	s.advance(t.Context())
	if len(s.machines) != 3 {
		t.Errorf("pool is %d after one install, want 3", len(s.machines))
	}
}

// TestStationMACsAreLocallyAdministered: invented hardware must not claim a
// real vendor's address space. 02: is the locally administered range, which is
// what something that does not exist is entitled to.
func TestStationMACsAreLocallyAdministered(t *testing.T) {
	s := newStationSim("http://x", "t", "sta", 8, 0, false, newCredStore())
	for mac := range s.machines {
		if mac[:3] != "02:" {
			t.Errorf("MAC %s is outside the locally administered range", mac)
		}
	}
}
