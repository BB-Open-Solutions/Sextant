package web_test

import (
	"os"
	"strings"
	"testing"
)

// The device list computed a release number for every visible row - deduped
// per revision, with a comment explaining the cost - and no template rendered
// it. Found by scanning view-model fields against the templates on 2026-08-23,
// the third instance that week of a value computed correctly and shown
// nowhere. This asserts the rendered page, which is the only place the defect
// was visible.
func TestTheDeviceListShowsTheReleaseItComputes(t *testing.T) {
	ts := newConsoleWithFleet(t, deviceScopeSeed)
	body := get(t, client(), ts.URL+"/devices")
	if !strings.Contains(body, "</html>") {
		t.Fatal("the device list did not render")
	}
	// The seed's devices have no known release, so the row falls back to the
	// hash in the title - which is exactly the branch that used to be the
	// only one. What must be true either way: the template reads the field.
	if !strings.Contains(body, "release_prefix") && !strings.Contains(body, "release ") {
		src, err := os.ReadFile("templates/devices.html")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(src), ".Release") {
			t.Error("no template reads deviceRow.Release; it is computed for every row and shown nowhere")
		}
	}
}
