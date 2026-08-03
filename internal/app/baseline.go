package app

import (
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// baseline.go: design 0008 - one computed verdict per device, derived at
// render time from state the console already holds. Two states only
// (compliant / needs attention); a failing verdict names exactly which
// criteria fail. No stored compliance state, no policy engine.

// Baseline failure labels; templates and the CSV print them verbatim.
const (
	BaselineRecency  = "no recent check-in"
	BaselineDrift    = "profile drift"
	BaselinePosture  = "security posture"
	BaselineRevision = "behind ring revision"
)

// The catalog keys that express the posture targets (design 0001). The
// posture wizard reads the same two; they live here so the wizard and the
// baseline cannot diverge on what "posture is targeted" means.
const (
	KeySecureBoot = "secureboot.enable"
	KeyTPM2       = "diskUnlock.tpm2.enable"
)

// Baseline is one device's verdict. Failures is empty when compliant.
type Baseline struct {
	Compliant bool
	Failures  []string
}

// BaselineJudge answers the verdict for many devices against one fleet
// snapshot. Build it once per render: the drifted-device set is derived
// from policies x assignments up front, not rediscovered per row.
type BaselineJudge struct {
	f       *fleet.Fleet
	drifted map[string]bool
	now     time.Time
}

// NewBaselineJudge precomputes which devices are reached by a policy whose
// profile stamp is behind the overlay's profile - the policies page's
// "reapply" state (profileState). Hand-edited and conflicting policies are
// deliberate operator choices, not drift, and do not count.
func NewBaselineJudge(f *fleet.Fleet, profiles *fleet.Profiles, now time.Time) *BaselineJudge {
	j := &BaselineJudge{f: f, drifted: map[string]bool{}, now: now}
	stale := map[string]bool{}
	for id, pol := range f.Policies {
		name, _, ok := strings.Cut(pol.Profile, "@")
		if !ok {
			continue
		}
		if src, has := profiles.Get(name); has && pol.Profile != src.Provenance() {
			stale[id] = true
		}
	}
	for _, a := range f.Assignments {
		if !stale[a.Policy] {
			continue
		}
		for _, tag := range j.f.AssignmentDevices(a) {
			j.drifted[tag] = true
		}
	}
	return j
}

// Verdict judges one device. hasStatus mirrors the inventory lookup: a
// never-seen device fails recency (and drift if config says so); posture
// and revision need a report to be judged at all. A reported-but-unknown
// posture fails when the ceremony is targeted - an unproven enrolment is
// not a pass. Retired devices return an empty verdict (nothing to judge).
func (j *BaselineJudge) Verdict(tag string, st StatusView, hasStatus bool) Baseline {
	dev, ok := j.f.Devices[tag]
	// A retired device is parked, and a provisional one has not been installed
	// yet. Neither has a baseline to fail. Judging a provisional device used to
	// report a recency failure the moment it was enrolled - "never seen" is
	// true, but for a machine that is still being imaged it describes the
	// process, not a problem. Until this state existed the two were
	// indistinguishable.
	if !ok || dev.Retired() || dev.Provisional() {
		return Baseline{}
	}
	var fails []string
	// Recency judges ABSENCE, not offline: a laptop on vacation is fine
	// (operator decision 2026-07-29, InactiveWindow). Only a device unseen
	// for over the window - or never seen - fails this criterion.
	if !hasStatus || j.now.Sub(st.LastSeen) > observed.InactiveWindow {
		fails = append(fails, BaselineRecency)
	}
	if j.drifted[tag] {
		fails = append(fails, BaselineDrift)
	}
	resolved := j.f.ResolveValues(tag)
	want := func(key string) bool {
		v, ok := resolved[key]
		b, _ := v.(bool)
		return ok && b
	}
	if want(KeySecureBoot) || want(KeyTPM2) {
		okSB := !want(KeySecureBoot) || st.SB == observed.SBEnforcing
		okTPM2 := !want(KeyTPM2) || st.TPM2 == observed.TPM2Enrolled
		if !hasStatus || !okSB || !okTPM2 {
			fails = append(fails, BaselinePosture)
		}
	}
	// Same judgement the incident detector uses: compare against the ring's
	// pinned revision; a device following HEAD ("" target) cannot be behind.
	if target := TargetRevision(j.f, dev); hasStatus && target != "" && st.Revision != target {
		fails = append(fails, BaselineRevision)
	}
	return Baseline{Compliant: len(fails) == 0, Failures: fails}
}
