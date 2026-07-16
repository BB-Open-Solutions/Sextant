package web_test

import (
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestDeviceLifecycleFromConsole(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// Update class and assigned user. The class comes from the controlled
	// vocabulary now, so an out-of-vocab value (e.g. "kiosk") is refused.
	if resp := post("/devices/lt-1/update", url.Values{"setclass": {"1"}, "class": {"kiosk"}}); resp.StatusCode != 400 {
		t.Fatalf("out-of-vocab class = %d, want 400", resp.StatusCode)
	}
	if resp := post("/devices/lt-1/update", url.Values{"setclass": {"1"}, "class": {"desktop"}}); resp.StatusCode != 303 {
		t.Fatalf("set class = %d", resp.StatusCode)
	}
	if resp := post("/devices/lt-1/update", url.Values{"setuser": {"1"}, "assignedUser": {"ada"}}); resp.StatusCode != 303 {
		t.Fatalf("assign = %d", resp.StatusCode)
	}
	d := cfg.Fleet().Devices["lt-1"]
	if d.Class != "desktop" || d.AssignedUser != "ada" {
		t.Fatalf("device = %+v", d)
	}
	if resp := post("/devices/lt-1/update", url.Values{}); resp.StatusCode != 400 {
		t.Fatalf("empty update = %d, want 400", resp.StatusCode)
	}

	// Retire: badge renders, double retire 400, reactivate restores.
	if resp := post("/devices/lt-1/retire", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("retire = %d", resp.StatusCode)
	}
	if !cfg.Fleet().Devices["lt-1"].Retired() {
		t.Fatal("not retired")
	}
	page, _ := client().Get(ts.URL + "/devices/lt-1")
	body, _ := io.ReadAll(page.Body)
	page.Body.Close()
	if !strings.Contains(string(body), "retired") {
		t.Error("retired badge missing")
	}
	if resp := post("/devices/lt-1/retire", url.Values{}); resp.StatusCode != 400 {
		t.Fatalf("double retire = %d, want 400", resp.StatusCode)
	}
	if resp := post("/devices/lt-1/reactivate", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("reactivate = %d", resp.StatusCode)
	}
	if cfg.Fleet().Devices["lt-1"].Retired() {
		t.Fatal("still retired")
	}

	// Remove unenrolls.
	if resp := post("/devices/lt-1/remove", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("remove = %d", resp.StatusCode)
	}
	if _, ok := cfg.Fleet().Devices["lt-1"]; ok {
		t.Fatal("device survived removal")
	}
}

// TestGroupAllowedClassesGuardrailFromConsole drives the AllowedClasses
// guardrail end to end from the console: restrict a group to laptops, then a
// reclassify that would put a disallowed class in the group is refused (400)
// and the device keeps its class.
func TestGroupAllowedClassesGuardrailFromConsole(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(path string, form url.Values) *http.Response {
		t.Helper()
		form.Set("csrf", "dev-csrf")
		resp, err := client().PostForm(ts.URL+path, form)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	// Classify the seed device so the group can be narrowed to laptops.
	if resp := post("/devices/lt-1/update", url.Values{"setclass": {"1"}, "class": {"laptop"}}); resp.StatusCode != 303 {
		t.Fatalf("set class = %d", resp.StatusCode)
	}
	// Restrict the pilot group to laptops via the groups form.
	if resp := post("/groups/pilot/update", url.Values{"setallowed": {"1"}, "allowedClass": {"laptop"}}); resp.StatusCode != 303 {
		t.Fatalf("set allowed classes = %d", resp.StatusCode)
	}
	if got := cfg.Fleet().Groups["pilot"].AllowedClasses; len(got) != 1 || got[0] != "laptop" {
		t.Fatalf("allowed classes = %v", got)
	}
	// Reclassifying the in-group device to a disallowed class is refused.
	if resp := post("/devices/lt-1/update", url.Values{"setclass": {"1"}, "class": {"server"}}); resp.StatusCode != 400 {
		t.Fatalf("guardrail reclassify = %d, want 400", resp.StatusCode)
	}
	if got := cfg.Fleet().Devices["lt-1"].Class; got != "laptop" {
		t.Fatalf("class changed despite guardrail: %q", got)
	}
}
