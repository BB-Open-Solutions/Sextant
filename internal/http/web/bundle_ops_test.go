package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

// TestBundleApply: the settings page renders the seeded bundle card, and
// applying it writes the bundle's settings at the scope, with an exposed knob
// override honoured. Keys that are not exposed take the bundle's value.
func TestBundleApply(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	resp, _ := c.Get(ts.URL + "/settings?scope=group:pilot")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if !strings.Contains(page, "Secure workplace") {
		t.Fatal("bundle card not rendered")
	}
	if !strings.Contains(page, `name="v:apps.retries"`) {
		t.Fatal("exposed knob not rendered")
	}

	// Apply at the pilot group, overriding the exposed retries knob to 7.
	resp, err := c.PostForm(ts.URL+"/settings/bundle/secure-workplace/apply", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"group:pilot"}, "v:apps.retries": {"7"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("apply = %d", resp.StatusCode)
	}
	own, _, _ := cfg.Fleet().ScopeSettings("group:pilot")
	if own["apps.office"] != true || own["desktop"] != "gnome" {
		t.Fatalf("bundle settings not applied: %v", own)
	}
	// The exposed override won over the bundle's recommendation (3 -> 7).
	if v := own["apps.retries"]; v != float64(7) && v != 7 {
		t.Fatalf("exposed override not honoured: %v", own["apps.retries"])
	}

	// Unknown bundle is a client error.
	resp, _ = c.PostForm(ts.URL+"/settings/bundle/nope/apply", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}})
	resp.Body.Close()
	if resp.StatusCode != 400 {
		t.Fatalf("unknown bundle = %d, want 400", resp.StatusCode)
	}
}
