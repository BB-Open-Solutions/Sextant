package incident

import (
	"testing"
	"time"
)

// A configuration lag and a core lag are not the same event and must not carry
// the same weight. A setting that has not arrived yet is a warning; a machine
// running an older kernel and older hardening, weeks after the fleet moved, is
// an issue. Numbering both as "behind release N" said neither.

func coreObs(deployedCore, targetCore string, pinned time.Time) Observation {
	return Observation{
		Tag: "lt-1", Group: "laptops",
		Deployed: "aaa", Target: "bbb",
		DeployedRelease: 10, TargetRelease: 11,
		Head: "bbb", HeadRelease: 11,
		Online: true, LastSeen: time.Now(),
		DeployedCore: deployedCore, TargetCore: targetCore,
		TargetCorePinned: pinned,
	}
}

func find(t *testing.T, in []Incident, k Kind) (Incident, bool) {
	t.Helper()
	for _, i := range in {
		if i.Kind == k {
			return i, true
		}
	}
	return Incident{}, false
}

func TestCoreLagIsAWarningWhileFresh(t *testing.T) {
	now := time.Now()
	got := Detect([]Observation{coreObs("core-old", "core-new", now.Add(-2*24*time.Hour))}, now)

	inc, ok := find(t, got, CoreOutdated)
	if !ok {
		t.Fatal("a device on an older core raised no incident at all")
	}
	if inc.Severity != Warning {
		t.Fatalf("severity %v; a core update still rolling out is a warning, not an issue", inc.Severity)
	}
}

func TestCoreLagBecomesAnIssueOnceItPersists(t *testing.T) {
	now := time.Now()
	got := Detect([]Observation{coreObs("core-old", "core-new", now.Add(-CoreGrace-24*time.Hour))}, now)

	inc, ok := find(t, got, CoreOutdated)
	if !ok {
		t.Fatal("a long-standing core lag raised no incident")
	}
	if inc.Severity != Critical {
		t.Fatalf("severity %v; past the grace period an unpatched core is an issue", inc.Severity)
	}
	if inc.Detail == "" || inc.Action == "" {
		t.Fatal("the issue does not say what is wrong or where to look")
	}
}

// The config lag that comes with it stays a warning. Both fire, and they say
// different things - that separation is the whole point.
func TestConfigLagStaysAWarningAlongsideACoreIssue(t *testing.T) {
	now := time.Now()
	got := Detect([]Observation{coreObs("core-old", "core-new", now.Add(-CoreGrace-24*time.Hour))}, now)

	behind, ok := find(t, got, Behind)
	if !ok {
		t.Fatal("the configuration lag disappeared")
	}
	if behind.Severity != Warning {
		t.Fatalf("configuration lag severity %v; falling behind on settings is a warning", behind.Severity)
	}
}

// Same core, different config revision: settings only. No core incident, or
// every ordinary edit would look like a system upgrade.
func TestSameCoreRaisesNoCoreIncident(t *testing.T) {
	now := time.Now()
	got := Detect([]Observation{coreObs("core-same", "core-same", now.Add(-90*24*time.Hour))}, now)

	if inc, ok := find(t, got, CoreOutdated); ok {
		t.Fatalf("two config revisions pinning the SAME core raised %q; that is a settings change, not a version change", inc.Title)
	}
}

// Unknown must never become an accusation: a revision this console cannot read
// says nothing about the device running it.
func TestUnknownCoreIsNotJudged(t *testing.T) {
	now := time.Now()
	for name, o := range map[string]Observation{
		"deployed unknown": coreObs("", "core-new", now.Add(-CoreGrace-time.Hour)),
		"target unknown":   coreObs("core-old", "", now.Add(-CoreGrace-time.Hour)),
		"both unknown":     coreObs("", "", now.Add(-CoreGrace-time.Hour)),
	} {
		if inc, ok := find(t, Detect([]Observation{o}, now), CoreOutdated); ok {
			t.Errorf("%s: raised %q from an unreadable core", name, inc.Title)
		}
	}
}

// No pin date means no clock to judge against, so it cannot escalate.
func TestCoreLagWithoutAPinDateStaysAWarning(t *testing.T) {
	now := time.Now()
	got := Detect([]Observation{coreObs("core-old", "core-new", time.Time{})}, now)

	inc, ok := find(t, got, CoreOutdated)
	if !ok {
		t.Fatal("a core lag with no pin date raised nothing")
	}
	if inc.Severity != Critical {
		return // warning is the expected outcome
	}
	t.Fatal("escalated to an issue without knowing how long the lag has lasted")
}
