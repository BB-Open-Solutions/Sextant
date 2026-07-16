package fleet

import (
	"fmt"
	"testing"
)

// synthetic10k builds a 10,000-device fleet with a realistic shape spread:
// 4 hardware profiles x 3 device classes x 5 top-level groups (each with a
// child), a couple of org policies with filters, and a small fraction of
// devices carrying their own overrides. The point of the benchmark suite:
// prove the 10k+ posture with measured numbers (docs/architecture/scale.md).
func synthetic10k() *Fleet {
	f := &Fleet{
		Version: 3,
		Org: &Scope{
			Settings: map[string]any{"desktop": "plasma", "apps.office.enable": true},
			Enforced: []string{"apps.office.enable"},
		},
		Groups:  map[string]Group{},
		Devices: map[string]Device{},
		Policies: map[string]Policy{
			"harden": {Settings: map[string]any{"firewall.strict": true}},
			"kiosk":  {Settings: map[string]any{"kiosk.enable": true}},
		},
		Filters: map[string]Filter{
			"kiosks": {Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: "kiosk"}}},
		},
		Assignments: []Assignment{
			{Policy: "harden", Target: "org"},
			{Policy: "kiosk", Target: "org", Filter: "kiosks"},
		},
	}
	hw := []string{"t495", "t14g3", "nuc8", "elitedesk"}
	classes := []string{"laptop", "desktop", "kiosk"}
	for g := range 5 {
		parent := fmt.Sprintf("site-%d", g)
		f.Groups[parent] = Group{Settings: map[string]any{"site.id": g}}
		f.Groups[parent+"-front"] = Group{Parent: parent}
	}
	for i := range 10000 {
		site := fmt.Sprintf("site-%d", i%5)
		if i%2 == 0 {
			site += "-front"
		}
		d := Device{
			Groups:   []string{site},
			Hardware: hw[i%len(hw)],
			Class:    classes[i%len(classes)],
		}
		// ~1% of devices carry a device-level override: each becomes (and
		// must become) its own shape.
		if i%100 == 0 {
			d.Settings = map[string]any{"special.flag": i}
		}
		f.Devices[fmt.Sprintf("dev-%05d", i)] = d
	}
	return f
}

func BenchmarkEquivalenceClasses10k(b *testing.B) {
	f := synthetic10k()
	b.ResetTimer()
	for b.Loop() {
		f.EquivalenceClasses()
	}
}

func BenchmarkRepresentatives10k(b *testing.B) {
	f := synthetic10k()
	b.ResetTimer()
	for b.Loop() {
		f.Representatives()
	}
}

func BenchmarkResolveOneOf10k(b *testing.B) {
	f := synthetic10k()
	b.ResetTimer()
	for b.Loop() {
		f.Resolve("dev-04217")
	}
}

// TestSynthetic10kShapeCount pins the sampling win to a number: 10,000
// devices collapse to a bounded set of configuration shapes, so the
// interactive gate evaluates ~this many hosts instead of 10,000. The exact
// count is asserted as a CEILING - growth here means the partitioner started
// splitting spuriously (or the synthetic fleet changed), both worth seeing.
func TestSynthetic10kShapeCount(t *testing.T) {
	f := synthetic10k()
	classes := f.EquivalenceClasses()
	total := 0
	for _, m := range classes {
		total += len(m)
	}
	if total != 10000 {
		t.Fatalf("partition lost devices: %d", total)
	}
	// The synthetic assignment is periodic - (i%4 hw, i%3 class, i%10 group)
	// yields lcm(4,3,10)=60 distinct base shapes - plus 100 one-off
	// device overrides (i%100==0, each its own shape) = 160.
	if len(classes) != 160 {
		t.Fatalf("shape count = %d, want 160 (60 base shapes + 100 overrides)", len(classes))
	}
	reps := f.Representatives()
	if len(reps) != 160 {
		t.Fatalf("representatives = %d, want 160", len(reps))
	}
	t.Logf("10k devices -> %d shapes: interactive gate evaluates %d hosts instead of 10000 (62x fewer)",
		len(classes), len(reps))
}
