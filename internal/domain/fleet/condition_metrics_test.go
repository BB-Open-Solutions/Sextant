package fleet

import (
	"sort"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// A condition naming a metric the observed plane never supplies can never
// hold, and unknown is never a violation - so the policy looks like governance
// and does nothing, with nothing on screen saying so. That is the "silently
// ignored" this validation exists to prevent, and it was accepted on save
// until 2026-08-21.
func TestAConditionOnAnUnknownMetricIsRefused(t *testing.T) {
	err := Condition{Metric: "nonsense.metric", Op: ">=", Value: 1}.Valid()
	if err == nil {
		t.Fatal("a condition on a metric that does not exist was accepted")
	}
	if !strings.Contains(err.Error(), "disk.free_percent") {
		t.Errorf("the refusal does not say what IS available: %v", err)
	}
	if err := (Condition{Metric: "disk.free_percent", Op: ">=", Value: 15}).Valid(); err != nil {
		t.Fatalf("a real metric was refused: %v", err)
	}
	// The operator check still stands, and still names the alternatives.
	if err := (Condition{Metric: "disk.free_percent", Op: "maybe"}).Valid(); err == nil {
		t.Error("an unknown operator was accepted")
	}
}

// The vocabulary is written out in the config plane because it must not import
// the observed one. This is what keeps the copy honest: every metric a device
// can report is accepted, and nothing else is.
func TestConditionMetricsMatchTheObservedPlane(t *testing.T) {
	// A usage reading with every dimension populated produces every metric
	// the observed plane knows how to make.
	full := observed.Usage{CPUPct: 20, MemUsedMB: 2048, MemTotalMB: 8192,
		DiskUsedGB: 100, DiskTotalGB: 500}
	m := full.Metrics()
	produced := make([]string, 0, len(m))
	for k := range m {
		produced = append(produced, k)
	}
	sort.Strings(produced)

	accepted := ConditionMetricNames()
	if strings.Join(produced, ",") != strings.Join(accepted, ",") {
		t.Fatalf("the vocabularies have drifted:\n  observed produces %v\n  fleet accepts   %v", produced, accepted)
	}
}
