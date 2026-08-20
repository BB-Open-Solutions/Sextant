package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
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
