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

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/adapters/git"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// A fleet with two models in one group: exactly the case the page exists for.
const hardwareFleet = `{
  "version": 3,
  "org": {"settings": {}},
  "groups": {"infra": {}},
  "devices": {
    "lt-lenovo": {"groups": ["infra"], "hardware": "lenovo-t495s"},
    "lt-dell":   {"groups": ["infra"], "hardware": "dell-latitude-5440"}
  }
}`

// End to end through the page: configure one model, and the setting reaches
// that model's device and no other. This is the whole point of the feature, so
// it is asserted against the resolver rather than against the form succeeding.
func TestConfiguringAModelFromThePage(t *testing.T) {
	ts := newConsoleWithFleet(t, hardwareFleet)
	c := client()

	body := get(t, c, ts.URL+"/hardware")
	for _, want := range []string{"lenovo-t495s", "dell-latitude-5440"} {
		if !strings.Contains(body, want) {
			t.Fatalf("the page does not list %s", want)
		}
	}

	resp, err := c.PostForm(ts.URL+"/hardware/lenovo-t495s/configure", url.Values{
		"csrf":     {"dev-csrf"},
		"settings": {"netbird.enable = true"},
		"target":   {"group:infra"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("configure = %d, want a redirect", resp.StatusCode)
	}

	// The Lenovo's own page shows the setting; the Dell's does not. Read
	// through the console, because a fleet document that is right and a
	// console that does not show it is the failure this page exists to fix.
	if lenovo := get(t, c, ts.URL+"/devices/lt-lenovo"); !strings.Contains(lenovo, "netbird.enable") {
		t.Fatal("the Lenovo does not show the setting its model was given")
	}
	if dell := get(t, c, ts.URL+"/devices/lt-dell"); strings.Contains(dell, "netbird.enable") {
		t.Fatal("the Dell shows a setting meant for another model")
	}

	// And the page now reports the model as configured, with the value in the
	// window so the next edit starts from what is there.
	body = get(t, c, ts.URL+"/hardware")
	if !strings.Contains(body, "netbird.enable = true") {
		t.Fatal("the page does not show the model's configuration back")
	}
}

// Saving an empty payload removes the configuration rather than leaving a
// policy that reaches devices and says nothing.
func TestClearingAModelFromThePage(t *testing.T) {
	ts := newConsoleWithFleet(t, hardwareFleet)
	c := client()
	set := func(settings string) {
		t.Helper()
		resp, err := c.PostForm(ts.URL+"/hardware/lenovo-t495s/configure", url.Values{
			"csrf": {"dev-csrf"}, "settings": {settings}, "target": {"org"}})
		if err != nil {
			t.Fatal(err)
		}
		b, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 303 {
			t.Fatalf("configure %q = %d: %s", settings, resp.StatusCode, b)
		}
	}
	set("netbird.enable = true")
	set("")
	if lenovo := get(t, c, ts.URL+"/devices/lt-lenovo"); strings.Contains(lenovo, "netbird.enable") {
		t.Fatal("clearing the model left the setting on its devices")
	}
}

func get(t *testing.T, c *http.Client, u string) string {
	t.Helper()
	resp, err := c.Get(u)
	if err != nil {
		t.Fatalf("%s: %v", u, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("%s: %d", u, resp.StatusCode)
	}
	return string(b)
}

// The overlay writes a disk layout and ordered steps for each model, and until
// 2026-08-21 nothing rendered either: the enrolment page promised
// "brand-specific guidance" and showed none, and the imaging catalog's most
// useful field existed only in json. This asserts the rendered page, because
// that is the half that was missing - the parsing always worked.
func TestTheHardwarePageShowsHowAModelIsImaged(t *testing.T) {
	ts := newConsoleWithHardwareProfiles(t, hardwareFleet, `[
	  {"name":"demo-vm","vendor":"QEMU","models":["Standard PC"],
	   "disko":"single ext4 root on /dev/vda (no encryption)",
	   "steps":[{"title":"Boot the installer","detail":"PXE-boot the VM"},
	            {"title":"Confirm the target disk","detail":"installs to /dev/vda"}]}
	]`)
	body := get(t, client(), ts.URL+"/hardware")

	for _, want := range []string{
		"single ext4 root on /dev/vda",
		"Boot the installer",
		"Confirm the target disk",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page does not show %q", want)
		}
	}
}

// newConsoleWithHardwareProfiles is newConsoleWithFleet plus the overlay's
// imaging catalog, which the hardware page reads and the other harness has no
// reason to carry.
func newConsoleWithHardwareProfiles(t *testing.T, fleetDoc, profiles string) *httptest.Server {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		out, err := exec.Command("git", append([]string{"-C", dir}, args...)...).CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	for name, body := range map[string]string{
		"fleet.json": fleetDoc, "catalog.json": seedCatalog,
		"hardware-profiles.json": profiles,
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
	srv, err := web.New(web.Services{Config: cfg}, web.DevSessions{}, true,
		nil, nil, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	srv.Routes(mux)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}
