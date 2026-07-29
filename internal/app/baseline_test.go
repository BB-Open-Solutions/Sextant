package app

import (
	"slices"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// baselineFleet: one pinned ring ("ring") holding lt-1, one profile-backed
// policy assigned org-wide, and posture targeted on lt-1 only - so every
// criterion can be flipped independently from here.
func baselineFleet(profileStamp string) *fleet.Fleet {
	return &fleet.Fleet{
		Version: 3,
		Groups:  map[string]fleet.Group{"ring": {Pin: "rev-good"}},
		Devices: map[string]fleet.Device{
			"lt-1": {Groups: []string{"ring"}, Hardware: "hw", Class: "laptop",
				Settings: map[string]any{KeySecureBoot: true, KeyTPM2: true}},
			"srv-1":   {Hardware: "hw", Class: "server"},
			"retired": {Hardware: "hw", State: fleet.DeviceRetired},
		},
		Policies: map[string]fleet.Policy{
			"laptop": {Settings: map[string]any{"x": true}, Profile: profileStamp},
		},
		Assignments: []fleet.Assignment{{Policy: "laptop", Target: "org"}},
	}
}

// baselineProfiles parses one overlay profile named "laptop".
func baselineProfiles(t *testing.T) *fleet.Profiles {
	t.Helper()
	ps, err := fleet.ParseProfiles([]byte(`[{"name":"laptop","settings":{"x":true}}]`))
	if err != nil {
		t.Fatal(err)
	}
	return ps
}

// goodStatus is a fully-compliant report for lt-1.
func goodStatus() StatusView {
	return StatusView{DeviceStatus: observed.DeviceStatus{
		Revision: "rev-good", SB: observed.SBEnforcing, TPM2: observed.TPM2Enrolled,
		LastSeen: time.Now()}, Online: true}
}

func TestBaselineVerdict(t *testing.T) {
	profiles := baselineProfiles(t)
	current := func(t *testing.T) string {
		p, ok := profiles.Get("laptop")
		if !ok {
			t.Fatal("profile fixture missing")
		}
		return p.Provenance()
	}

	t.Run("compliant", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		b := j.Verdict("lt-1", goodStatus(), true)
		if !b.Compliant || len(b.Failures) != 0 {
			t.Fatalf("want compliant, got %+v", b)
		}
	})

	t.Run("offline within the window stays compliant", func(t *testing.T) {
		// Vacation-proof (2026-07-29): offline is a neutral state; only
		// absence beyond InactiveWindow fails recency.
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		st := goodStatus()
		st.Online = false
		st.LastSeen = time.Now().Add(-7 * 24 * time.Hour)
		if b := j.Verdict("lt-1", st, true); !b.Compliant {
			t.Fatalf("week-offline laptop must stay compliant, got %+v", b)
		}
	})

	t.Run("recency fails past the inactive window", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		st := goodStatus()
		st.Online = false
		st.LastSeen = time.Now().Add(-observed.InactiveWindow - time.Hour)
		b := j.Verdict("lt-1", st, true)
		if b.Compliant || !slices.Contains(b.Failures, BaselineRecency) {
			t.Fatalf("want recency failure, got %+v", b)
		}
	})

	t.Run("never seen fails recency only judgeable criteria", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		b := j.Verdict("lt-1", StatusView{}, false)
		if b.Compliant {
			t.Fatalf("want attention, got %+v", b)
		}
		// Recency and (unprovable) posture fail; revision needs a report.
		if !slices.Contains(b.Failures, BaselineRecency) ||
			!slices.Contains(b.Failures, BaselinePosture) ||
			slices.Contains(b.Failures, BaselineRevision) {
			t.Fatalf("unexpected failures %v", b.Failures)
		}
	})

	t.Run("drift fails when the profile moved on", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet("laptop@stale"), profiles, time.Now())
		b := j.Verdict("lt-1", goodStatus(), true)
		if b.Compliant || !slices.Contains(b.Failures, BaselineDrift) {
			t.Fatalf("want drift failure, got %+v", b)
		}
	})

	t.Run("hand-made policy is not drift", func(t *testing.T) {
		f := baselineFleet("")
		j := NewBaselineJudge(f, profiles, time.Now())
		if b := j.Verdict("lt-1", goodStatus(), true); !b.Compliant {
			t.Fatalf("want compliant, got %+v", b)
		}
	})

	t.Run("posture fails on unenrolled tpm2", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		st := goodStatus()
		st.TPM2 = observed.TPM2Present
		b := j.Verdict("lt-1", st, true)
		if b.Compliant || !slices.Contains(b.Failures, BaselinePosture) {
			t.Fatalf("want posture failure, got %+v", b)
		}
	})

	t.Run("posture exempt when not targeted", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		st := goodStatus()
		st.Revision = "" // srv-1 follows HEAD: no target, cannot be behind
		b := j.Verdict("srv-1", st, true)
		if !b.Compliant {
			t.Fatalf("server without posture targets must pass, got %+v", b)
		}
	})

	t.Run("revision fails behind the ring pin", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		st := goodStatus()
		st.Revision = "rev-old"
		b := j.Verdict("lt-1", st, true)
		if b.Compliant || !slices.Contains(b.Failures, BaselineRevision) {
			t.Fatalf("want revision failure, got %+v", b)
		}
	})

	t.Run("retired is not judged", func(t *testing.T) {
		j := NewBaselineJudge(baselineFleet(current(t)), profiles, time.Now())
		b := j.Verdict("retired", StatusView{}, false)
		if b.Compliant || b.Failures != nil {
			t.Fatalf("want empty verdict, got %+v", b)
		}
	})
}
