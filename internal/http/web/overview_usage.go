package web

import (
	"strconv"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
)

// overview_usage.go: the fleet's LIVE resource use for the overview, aggregated
// from the utilisation each device reported at its last check-in. Unlike the
// capacity view (static, from specs) this is real usage, so it only counts
// devices that actually reported a reading. Kept a small pure function so the
// aggregation is testable without the whole handler.

// utilGauge is one live metric as a two-arc donut: UsedPct fills, the rest is
// free. Label is the used/total caption under the ring.
type utilGauge struct {
	UsedPct int
	Label   string
}

// utilization is the fleet's live use: the per-metric gauges plus how many
// devices contributed (Reporting == 0 means no device has reported yet, so the
// console shows "waiting for data" rather than a misleading 0%).
type utilization struct {
	Reporting int
	CPU       utilGauge
	RAM       utilGauge
	Disk      utilGauge
}

// fleetUtilization averages CPU and totals RAM/disk across the devices that
// reported a reading. RAM/disk percentages are of the fleet's summed capacity,
// so a few busy machines do not hide behind idle ones.
func fleetUtilization(status []app.StatusView) utilization {
	var (
		u                 utilization
		cpuSum            int
		memUsed, memTot   int
		diskUsed, diskTot int
	)
	for _, st := range status {
		if !st.Usage.Reported() {
			continue
		}
		u.Reporting++
		cpuSum += st.Usage.CPUPct
		memUsed += st.Usage.MemUsedMB
		memTot += st.Usage.MemTotalMB
		diskUsed += st.Usage.DiskUsedGB
		diskTot += st.Usage.DiskTotalGB
	}
	if u.Reporting == 0 {
		return u
	}
	u.CPU = utilGauge{UsedPct: cpuSum / u.Reporting, Label: "avg"}
	u.RAM = utilGauge{UsedPct: pctOf(memUsed, memTot), Label: gb(memUsed/1024) + " / " + gb(memTot/1024)}
	u.Disk = utilGauge{UsedPct: pctOf(diskUsed, diskTot), Label: gb(diskUsed) + " / " + gb(diskTot)}
	return u
}

// pctOf is used*100/total, clamped to 0..100, 0 when total is 0.
func pctOf(used, total int) int {
	if total <= 0 {
		return 0
	}
	p := used * 100 / total
	if p > 100 {
		return 100
	}
	if p < 0 {
		return 0
	}
	return p
}

// gb renders a whole-GB figure with its unit.
func gb(n int) string { return strconv.Itoa(n) + " GB" }
