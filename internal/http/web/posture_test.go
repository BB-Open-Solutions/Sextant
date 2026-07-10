package web

import (
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

func TestPostureView(t *testing.T) {
	f := &fleet.Fleet{
		Version: 3,
		Org:     &fleet.Scope{Settings: map[string]any{"secureboot.enable": true, "diskUnlock.tpm2.enable": true}},
		Devices: map[string]fleet.Device{"lt-1": {Hardware: "hw"}},
	}
	s := &Server{}
	// Enforcing + present -> next step is enroll TPM2.
	st := app.StatusView{DeviceStatus: observed.DeviceStatus{
		Tag: "lt-1", SB: observed.SBEnforcing, TPM2: observed.TPM2Present}}
	v := s.postureView(f, "lt-1", st)
	if !v.WantSB || !v.WantTPM2 || v.Step != observed.StepEnrollTPM2 || !v.Reported {
		t.Fatalf("view = %+v", v)
	}
	if v.Complete() || v.Warn() {
		t.Fatal("not complete, not warn at enroll step")
	}
	// No posture reported -> Reported false.
	v = s.postureView(f, "lt-1", app.StatusView{DeviceStatus: observed.DeviceStatus{Tag: "lt-1"}})
	if v.Reported {
		t.Fatal("empty posture reported as reported")
	}
	// Target off -> nothing wanted.
	f.Org.Settings = map[string]any{}
	v = s.postureView(f, "lt-1", st)
	if v.WantSB || v.WantTPM2 {
		t.Fatal("posture wanted when target off")
	}
}
