package web

import (
	"sort"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// overview_capacity.go: the fleet's hardware capacity for the overview. Kept out
// of the overview handler so the aggregation is a small, pure, testable unit.
// Only capacity is known (CPU cores / RAM / disk from captured specs), not live
// utilisation, so the donut breaks each metric down by hardware profile - "where
// the fleet's compute and storage sit" - with a switch on the page flipping
// which metric's total and donut show.

// capSeg is one donut slice: its length (Dash, a percent of the metric total,
// on an r=15.915 ring whose circumference is 100) and its cumulative Offset.
type capSeg struct {
	Label  string
	Value  int
	Dash   int
	Offset int
	Color  string
}

// capacity is the overview's hardware summary: per-metric totals and the
// per-profile donut segments for each.
type capacity struct {
	Cores, RAMGB, DiskGB     int
	CoreSeg, RAMSeg, DiskSeg []capSeg
	Devices                  int // active devices with a captured spec
}

// capPalette colours the donut slices (mint-forward, then warn/neutral).
var capPalette = []string{"#00d4a4", "#00b48a", "#7cebcb", "#c37d0d", "#5f5e5e", "#acc7ff"}

// fleetCapacity aggregates the non-retired devices that reported a hardware
// spec: it sums each metric and builds a per-profile breakdown (top profiles
// plus an "other" slice) for the donuts.
func fleetCapacity(f *fleet.Fleet) capacity {
	var cap capacity
	if f == nil {
		return cap
	}
	cores := map[string]int{}
	ram := map[string]int{}
	disk := map[string]int{}
	for _, d := range f.Devices {
		if d.Retired() || d.Spec == nil {
			continue
		}
		profile := d.Hardware
		if profile == "" {
			profile = "unknown"
		}
		cores[profile] += d.Spec.Cores
		ram[profile] += d.Spec.MemGB
		disk[profile] += d.Spec.DiskGB
		cap.Cores += d.Spec.Cores
		cap.RAMGB += d.Spec.MemGB
		cap.DiskGB += d.Spec.DiskGB
		cap.Devices++
	}
	cap.CoreSeg = segments(cores, cap.Cores)
	cap.RAMSeg = segments(ram, cap.RAMGB)
	cap.DiskSeg = segments(disk, cap.DiskGB)
	return cap
}

// segments turns a per-profile total into donut slices: sorted by value
// (largest first, ties by label for determinism), the top five kept and the
// rest folded into "other", each with a percent-length and a cumulative offset.
func segments(byProfile map[string]int, total int) []capSeg {
	if total == 0 {
		return nil
	}
	type kv struct {
		k string
		v int
	}
	items := make([]kv, 0, len(byProfile))
	for k, v := range byProfile {
		if v > 0 {
			items = append(items, kv{k, v})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].v != items[j].v {
			return items[i].v > items[j].v
		}
		return items[i].k < items[j].k
	})

	const top = 5
	if len(items) > top {
		other := 0
		for _, it := range items[top:] {
			other += it.v
		}
		items = append(items[:top:top], kv{"other", other})
	}

	segs := make([]capSeg, 0, len(items))
	offset := 0
	for i, it := range items {
		dash := it.v * 100 / total
		segs = append(segs, capSeg{
			Label:  it.k,
			Value:  it.v,
			Dash:   dash,
			Offset: -offset,
			Color:  capPalette[i%len(capPalette)],
		})
		offset += dash
	}
	return segs
}
