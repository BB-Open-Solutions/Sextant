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

// newEnrollConsole seeds a station with several discovered Lenovos so the
// guided flow and batch imaging can both be exercised.
func newEnrollConsole(t *testing.T) (*httptest.Server, *app.ConfigService, *memDisc) {
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
	mk := func(mac string) discovery.Discovered {
		return discovery.Discovered{MAC: mac, Phase: observed.Discovered,
			Vendor: "LENOVO", Model: "ThinkPad T495s", LastSeen: time.Unix(1000, 0)}
	}
	disc.sets[app.DefaultTenant+"|nuc-1"] = []discovery.Discovered{
		mk("aa:bb:cc:dd:ee:01"), mk("aa:bb:cc:dd:ee:02"), mk("aa:bb:cc:dd:ee:03"),
	}
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

func TestEnrollBatchPageShowsDevicesAndSuggestion(t *testing.T) {
	ts, _, _ := newEnrollConsole(t)
	c := client()

	// Landing: the station picker.
	resp, _ := c.Get(ts.URL + "/enroll")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "Imaging station") {
		t.Fatalf("enroll landing = %d", resp.StatusCode)
	}

	// With a station chosen: the batch form lists each discovered device with a
	// per-device CMDB-name input and the suggested profile chip.
	resp, _ = c.Get(ts.URL + "/enroll?station=nuc-1")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if !strings.Contains(s, "lenovo-t495s") {
		t.Fatalf("no profile suggestion on the batch page\n%s", s)
	}
	if !strings.Contains(s, `name-aabbccddee01`) {
		t.Fatal("per-device CMDB name input not rendered")
	}
	if !strings.Contains(s, "/enroll/nuc-1/batch") {
		t.Fatal("batch form action missing")
	}
}

func TestEnrollDiscoveredRemove(t *testing.T) {
	ts, _, disc := newEnrollConsole(t)
	c := client()

	resp, _ := c.PostForm(ts.URL+"/enroll/nuc-1/discovered/aa:bb:cc:dd:ee:02/remove",
		url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("discovered remove = %d, want 303", resp.StatusCode)
	}
	left, _ := disc.List(context.Background(), app.DefaultTenant, "nuc-1")
	for _, d := range left {
		if d.MAC == "aa:bb:cc:dd:ee:02" {
			t.Fatal("removed MAC still in the discovered set")
		}
	}
	if len(left) != 2 {
		t.Fatalf("discovered set = %d, want 2 after removing one", len(left))
	}
}

func TestEnrollBatchImagesNamedDevices(t *testing.T) {
	ts, cfg, disc := newEnrollConsole(t)
	c := client()

	// Batch-only: each selected device carries the CMDB name the operator typed.
	form := url.Values{"csrf": {"dev-csrf"},
		"hardware": {"lenovo-t495s"}, "class": {"laptop"}, "group": {"pilot"}}
	form["mac"] = []string{"aa:bb:cc:dd:ee:01", "aa:bb:cc:dd:ee:03"}
	form.Set("name-aabbccddee01", "kiosk-01")
	form.Set("name-aabbccddee03", "kiosk-03")
	resp, _ := c.PostForm(ts.URL+"/enroll/nuc-1/batch", form)
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("batch enroll = %d, want 303", resp.StatusCode)
	}
	devs := cfg.Fleet().Devices
	for _, tag := range []string{"kiosk-01", "kiosk-03"} {
		d, ok := devs[tag]
		if !ok {
			t.Fatalf("device %s not enrolled", tag)
		}
		if d.Hardware != "lenovo-t495s" || len(d.Groups) != 1 || d.Groups[0] != "pilot" {
			t.Fatalf("device %s wrong: %+v", tag, d)
		}
	}
	// The un-selected device (...02) was left on the station set.
	left, _ := disc.List(context.Background(), app.DefaultTenant, "nuc-1")
	if len(left) != 1 || left[0].MAC != "aa:bb:cc:dd:ee:02" {
		t.Fatalf("station set after batch = %v, want only ...02", left)
	}

	// A selected device with a blank name is rejected (no partial enroll).
	blank := url.Values{"csrf": {"dev-csrf"}, "hardware": {"lenovo-t495s"},
		"mac": {"aa:bb:cc:dd:ee:02"}}
	resp, _ = c.PostForm(ts.URL+"/enroll/nuc-1/batch", blank)
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("batch accepted a selected device with no name")
	}

	// A duplicate name across two selected devices is rejected.
	dup := url.Values{"csrf": {"dev-csrf"}, "hardware": {"lenovo-t495s"}}
	dup["mac"] = []string{"aa:bb:cc:dd:ee:02", "aa:bb:cc:dd:ee:01"}
	dup.Set("name-aabbccddee02", "same")
	dup.Set("name-aabbccddee01", "same")
	resp, _ = c.PostForm(ts.URL+"/enroll/nuc-1/batch", dup)
	resp.Body.Close()
	if resp.StatusCode == 303 {
		t.Fatal("batch accepted duplicate names")
	}
}
