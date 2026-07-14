package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func sv(cpu, memU, memT, diskU, diskT int) app.StatusView {
	return app.StatusView{DeviceStatus: observed.DeviceStatus{
		Usage: observed.Usage{CPUPct: cpu, MemUsedMB: memU, MemTotalMB: memT, DiskUsedGB: diskU, DiskTotalGB: diskT},
	}}
}

func TestFleetUtilization(t *testing.T) {
	// Two reporting devices + one that never reported (must be ignored).
	status := []app.StatusView{
		sv(20, 4096, 8192, 100, 500),            // 4/8 GB, 100/500
		sv(60, 4096, 8192, 300, 500),            // 4/8 GB, 300/500
		{DeviceStatus: observed.DeviceStatus{}}, // no usage
	}
	u := fleetUtilization(status)

	if u.Reporting != 2 {
		t.Fatalf("reporting = %d, want 2", u.Reporting)
	}
	if u.CPU.UsedPct != 40 { // avg(20,60)
		t.Fatalf("avg cpu = %d, want 40", u.CPU.UsedPct)
	}
	if u.RAM.UsedPct != 50 { // 8192/16384
		t.Fatalf("ram%% = %d, want 50", u.RAM.UsedPct)
	}
	if u.Disk.UsedPct != 40 { // 400/1000
		t.Fatalf("disk%% = %d, want 40", u.Disk.UsedPct)
	}
	if u.RAM.Label != "8 GB / 16 GB" {
		t.Fatalf("ram label = %q", u.RAM.Label)
	}
	if u.Disk.Label != "400 GB / 1000 GB" {
		t.Fatalf("disk label = %q", u.Disk.Label)
	}
}

func TestFleetUtilizationNoData(t *testing.T) {
	if u := fleetUtilization(nil); u.Reporting != 0 || u.CPU.UsedPct != 0 {
		t.Fatal("no reporting devices must yield an empty utilization")
	}
}

func TestPctOfClamps(t *testing.T) {
	if pctOf(10, 0) != 0 {
		t.Error("zero total must be 0%")
	}
	if pctOf(150, 100) != 100 {
		t.Error("over-100 must clamp to 100")
	}
	if pctOf(25, 100) != 25 {
		t.Error("normal ratio wrong")
	}
}
