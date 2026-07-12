package web_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// memDisc is an in-memory ports.DiscoveredStore for the enroll test.
type memDisc struct {
	sets map[string][]discovery.Discovered
}

func (m *memDisc) Report(_ context.Context, tenant, station string, d []discovery.Discovered, _ time.Time) error {
	m.sets[tenant+"|"+station] = d
	return nil
}
func (m *memDisc) List(_ context.Context, tenant, station string) ([]discovery.Discovered, error) {
	return m.sets[tenant+"|"+station], nil
}
func (m *memDisc) Remove(_ context.Context, tenant, station, mac string) error {
	cur := m.sets[tenant+"|"+station]
	out := cur[:0:0]
	for _, d := range cur {
		if d.MAC != mac {
			out = append(out, d)
		}
	}
	m.sets[tenant+"|"+station] = out
	return nil
}

const seedStationFleet = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"pilot": {}},
  "stations": {"nuc-1": {"description": "test bench"}}
}`

const seedHardwareProfiles = `[
  {"name":"lenovo-t495s","vendor":"Lenovo","models":["ThinkPad T495s","20QH"],
   "steps":[{"title":"Enter firmware","key":"Enter"}]},
  {"name":"hp-probook-440","vendor":"HP","models":["ProBook 440"]}
]`

func newStationConsole(t *testing.T) (*httptest.Server, *app.ConfigService, *memDisc) {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{
		"fleet.json":             seedStationFleet,
		"hardware-profiles.json": seedHardwareProfiles,
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	run("add", ".")
	run("-c", "user.name=t", "-c", "user.email=t@t", "commit", "-q", "-m", "seed")
	repo, err := git.Open(dir, "")
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := app.NewConfigService(repo,
		ports.GateFunc(func(context.Context, string, []string) error { return nil }))
	if err != nil {
		t.Fatal(err)
	}
	disc := &memDisc{sets: map[string][]discovery.Discovered{}}
	// One discovered Lenovo, with specs, on station nuc-1 (default tenant).
	disc.sets[app.DefaultTenant+"|nuc-1"] = []discovery.Discovered{{
		MAC: "aa:bb:cc:dd:ee:01", Phase: observed.Discovered,
		Vendor: "LENOVO", Model: "ThinkPad T495s Gen 1", Serial: "PF-123",
		CPU: "AMD Ryzen 7", Cores: 8, MemGB: 32, DiskGB: 512, Firmware: "UEFI 1.2",
		LastSeen: time.Unix(1000, 0),
	}}
	srv, err := web.New(web.Services{
		Config:    cfg,
		Discovery: app.NewDiscoveryService(disc, clockNow{}, ""),
	}, web.DevSessions{}, true, nil, nil, nil,
		slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts, cfg, disc
}

func TestStationEnrollCapturesSpecsAndValidatesProfile(t *testing.T) {
	ts, cfg, disc := newStationConsole(t)
	c := client()

	// Page offers the profile as a dropdown and suggests it from the make.
	resp, _ := c.Get(ts.URL + "/enroll?station=nuc-1")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("page = %d", resp.StatusCode)
	}
	if !strings.Contains(s, `<select name="hardware"`) {
		t.Fatal("hardware profile is not a dropdown")
	}
	if !strings.Contains(s, `value="lenovo-t495s" selected`) {
		t.Fatalf("lenovo profile not suggested from discovered model\n%s", s)
	}

	// An unknown profile is rejected (cannot enroll onto a profile the
	// generator can't build).
	bad := url.Values{"csrf": {"dev-csrf"}, "mac": {"aa:bb:cc:dd:ee:01"},
		"tag": {"nuc-lab-1"}, "hardware": {"made-up"}, "class": {"laptop"}}
	resp, _ = c.PostForm(ts.URL+"/enroll/nuc-1/image", bad)
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("enroll accepted an unpublished hardware profile")
	}
	if _, ok := cfg.Fleet().Devices["nuc-lab-1"]; ok {
		t.Fatal("device created despite invalid profile")
	}

	// A valid profile enrolls the device AND stores the captured specs.
	good := url.Values{"csrf": {"dev-csrf"}, "mac": {"aa:bb:cc:dd:ee:01"},
		"tag": {"nuc-lab-1"}, "hardware": {"lenovo-t495s"}, "class": {"laptop"}, "group": {"pilot"}}
	resp, _ = c.PostForm(ts.URL+"/enroll/nuc-1/image", good)
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("valid enroll = %d, want 303", resp.StatusCode)
	}
	dev, ok := cfg.Fleet().Devices["nuc-lab-1"]
	if !ok {
		t.Fatal("device not enrolled")
	}
	if dev.Hardware != "lenovo-t495s" {
		t.Fatalf("hardware = %q", dev.Hardware)
	}
	if dev.Spec == nil {
		t.Fatal("hardware specs not captured onto the device")
	}
	if dev.Spec.Model != "ThinkPad T495s Gen 1" || dev.Spec.Cores != 8 || dev.Spec.MemGB != 32 || dev.Spec.Serial != "PF-123" {
		t.Fatalf("captured specs wrong: %+v", dev.Spec)
	}
	if dev.ITAM.Serial != "PF-123" {
		t.Fatalf("ITAM serial not set: %q", dev.ITAM.Serial)
	}
	// The MAC is dropped from the station's discovered set once enrolled.
	if got, _ := disc.List(context.Background(), app.DefaultTenant, "nuc-1"); len(got) != 0 {
		t.Fatalf("MAC not removed from station set after enroll: %v", got)
	}
}
