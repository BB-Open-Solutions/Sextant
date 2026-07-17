package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// guardFleet: one laptop enforcing Secure Boot via a group setting, one
// laptop that never enrolled it.
const guardFleet = `{
  "version": 3,
  "org": {},
  "groups": {"pilot": {"settings": {"secureboot.enable": true}}},
  "devices": {
    "sb-on":  {"groups": ["pilot"], "hardware": "hw"},
    "sb-off": {"groups": [], "hardware": "hw"}
  }
}`

func newGuardStack(t *testing.T) (*ConfigService, *InventoryService) {
	t.Helper()
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(guardFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	st := newMemStatus()
	inv := NewInventoryService(st, nil, clockAt{time.Now()}, "")
	// sb-on reports enforcing firmware; sb-off reports off.
	if err := inv.CheckIn(context.Background(), observed.CheckIn{Tag: "sb-on", SB: observed.SBEnforcing}, nil); err != nil {
		t.Fatal(err)
	}
	if err := inv.CheckIn(context.Background(), observed.CheckIn{Tag: "sb-off", SB: observed.SBOff}, nil); err != nil {
		t.Fatal(err)
	}
	return cfg, inv
}

func TestGuardRefusesDisablingEnforcedSecureBoot(t *testing.T) {
	cfg, inv := newGuardStack(t)
	ctx := context.Background()

	// Turning the group's Secure Boot off while sb-on enforces: refused.
	err := GuardBrickingSettings(ctx, cfg, inv, "group:pilot",
		[]SettingChange{{Key: "secureboot.enable", RawValue: "false"}})
	if err == nil || !strings.Contains(err.Error(), "sb-on") {
		t.Fatalf("expected refusal naming sb-on, got %v", err)
	}
	// Clearing the very scope that supplies the "on": same brick, refused.
	err = GuardBrickingSettings(ctx, cfg, inv, "group:pilot",
		[]SettingChange{{Key: "secureboot.enable", Clear: true}})
	if err == nil {
		t.Fatal("expected refusal for clearing the sourcing scope")
	}
}

func TestGuardAllowsSafeSecureBootChanges(t *testing.T) {
	cfg, inv := newGuardStack(t)
	ctx := context.Background()

	// A device whose posture is NOT enforcing may turn it off.
	if err := GuardBrickingSettings(ctx, cfg, inv, "device:sb-off",
		[]SettingChange{{Key: "secureboot.enable", RawValue: "false"}}); err != nil {
		t.Fatalf("non-enforcing device blocked: %v", err)
	}
	// Turning Secure Boot ON is never a brick.
	if err := GuardBrickingSettings(ctx, cfg, inv, "group:pilot",
		[]SettingChange{{Key: "secureboot.enable", RawValue: "true"}}); err != nil {
		t.Fatalf("enable blocked: %v", err)
	}
	// Clearing at a scope that is NOT the source changes nothing: allowed.
	if err := GuardBrickingSettings(ctx, cfg, inv, "device:sb-on",
		[]SettingChange{{Key: "secureboot.enable", Clear: true}}); err != nil {
		t.Fatalf("no-op clear blocked: %v", err)
	}
	// Unrelated keys pass untouched.
	if err := GuardBrickingSettings(ctx, cfg, inv, "group:pilot",
		[]SettingChange{{Key: "desktop.plasma.enable", RawValue: "false"}}); err != nil {
		t.Fatalf("unrelated key blocked: %v", err)
	}
	// No observed plane configured: guard stands down, saves keep working.
	if err := GuardBrickingSettings(ctx, cfg, nil, "group:pilot",
		[]SettingChange{{Key: "secureboot.enable", RawValue: "false"}}); err != nil {
		t.Fatalf("nil inventory should disable the guard: %v", err)
	}
}

// coherenceFleet: one device already on GNOME via its group.
const coherenceFleet = `{
  "version": 3,
  "org": {},
  "groups": {"kantoor": {"settings": {"desktop.gnome.enable": true}}},
  "devices": {"wk-1": {"groups": ["kantoor"], "hardware": "hw"}}
}`

func TestGuardExclusiveSettings(t *testing.T) {
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "fleet.json"), []byte(coherenceFleet), 0o644); err != nil {
		t.Fatal(err)
	}
	sh(t, dir, "add", "fleet.json")
	sh(t, dir, "-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := NewConfigService(repo, ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}

	// Turning Plasma on while GNOME resolves true for the device: invalid.
	err = GuardExclusiveSettings(cfg, "group:kantoor",
		[]SettingChange{{Key: "desktop.plasma.enable", RawValue: "true"}})
	if err == nil || !strings.Contains(err.Error(), "cannot both be enabled") {
		t.Fatalf("both desktops on was not refused: %v", err)
	}
	// The valid switch: Plasma on AND GNOME off in the same save.
	if err := GuardExclusiveSettings(cfg, "group:kantoor", []SettingChange{
		{Key: "desktop.plasma.enable", RawValue: "true"},
		{Key: "desktop.gnome.enable", RawValue: "false"},
	}); err != nil {
		t.Fatalf("explicit switch refused: %v", err)
	}
	// Untouched pairs never block.
	if err := GuardExclusiveSettings(cfg, "group:kantoor",
		[]SettingChange{{Key: "apps.office.enable", RawValue: "true"}}); err != nil {
		t.Fatalf("unrelated save blocked: %v", err)
	}
}
