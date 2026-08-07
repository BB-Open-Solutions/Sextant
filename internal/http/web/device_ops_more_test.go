package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

// device_ops_more_test.go covers two console POST handlers that were at 0%:
// creating a group out of selected devices, and setting a value at device
// scope. Both write configuration through the gate, so a handler that
// accepts what it should refuse writes it to git.

func TestCreateAGroupFromSelectedDevices(t *testing.T) {
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

	// A group with no devices is refused. The whole point of this action is
	// "these machines, together"; an empty one is a group the operator will
	// look for on the devices page and not find.
	if resp := post("/devices/group", url.Values{"name": {"finance"}}); resp.StatusCode != 400 {
		t.Errorf("group with no devices = %d, want 400", resp.StatusCode)
	}
	// And a nameless group is refused rather than given a generated name.
	if resp := post("/devices/group", url.Values{"tags": {"lt-1"}}); resp.StatusCode != 400 {
		t.Errorf("group with no name = %d, want 400", resp.StatusCode)
	}

	resp := post("/devices/group", url.Values{"name": {"finance"}, "tags": {"lt-1"}})
	if resp.StatusCode != 303 {
		t.Fatalf("create group = %d", resp.StatusCode)
	}
	f := cfg.Fleet()
	if _, ok := f.Groups["finance"]; !ok {
		t.Fatalf("the group was not created: %v", f.Groups)
	}
	// The devices have to actually be in it, or the operator gets an empty
	// group and no error.
	var found bool
	for _, g := range f.Devices["lt-1"].Groups {
		if g == "finance" {
			found = true
		}
	}
	if !found {
		t.Errorf("lt-1 is not in the new group: %v", f.Devices["lt-1"].Groups)
	}

	// A device that does not exist must not silently create a group with a
	// dangling member.
	if resp := post("/devices/group", url.Values{"name": {"ghosts"}, "tags": {"no-such-device"}}); resp.StatusCode == 303 {
		if _, ok := cfg.Fleet().Groups["ghosts"]; ok {
			t.Error("a group was created around a device that does not exist")
		}
	}
}

func TestSetASettingAtDeviceScope(t *testing.T) {
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

	// An empty key is refused: writing one would put a nameless entry in the
	// fleet document that no catalog lookup can ever resolve.
	if resp := post("/devices/lt-1/settings", url.Values{"value": {"true"}}); resp.StatusCode != 400 {
		t.Errorf("empty key = %d, want 400", resp.StatusCode)
	}

	resp := post("/devices/lt-1/settings", url.Values{"key": {"apps.office"}, "value": {"true"}})
	if resp.StatusCode != 303 {
		t.Fatalf("set device setting = %d", resp.StatusCode)
	}
	got := cfg.Fleet().Devices["lt-1"].Settings["apps.office"]
	// parseValue must have made this a real boolean rather than the string
	// "true": the nix generator types its inputs, and a string where a bool
	// belongs fails evaluation at the gate rather than here.
	if b, ok := got.(bool); !ok || !b {
		t.Errorf("stored %#v (%T), want the boolean true", got, got)
	}

	// Audit finding L2, closed 2026-08-07. This handler used to take a
	// free-form key and guess the value's type from the string's shape,
	// while the settings page (which iterates cat.Entries) and the API
	// (which looks the key up) both went through the catalog. A typo became
	// a setting that governs nothing, in the document whose purpose is to
	// say what governs.
	if resp := post("/devices/lt-1/settings",
		url.Values{"key": {"apps.nonexistent"}, "value": {"true"}}); resp.StatusCode == 303 {
		t.Error("a setting outside the catalog was accepted")
	}
	if _, ok := cfg.Fleet().Devices["lt-1"].Settings["apps.nonexistent"]; ok {
		t.Error("the unknown key reached the fleet document")
	}

	// The other half of L2: the TYPE comes from the catalog too. Guessing it
	// is how a list-valued setting once became a one-element list holding
	// the text "[a b]", and usbDevices.allowlist is list-valued - so the
	// silent case reconfigured a security control.
	if resp := post("/devices/lt-1/settings",
		url.Values{"key": {"apps.retries"}, "value": {"not-a-number"}}); resp.StatusCode == 303 {
		t.Error("a value that does not fit the catalog type was accepted")
	}
	if resp := post("/devices/lt-1/settings",
		url.Values{"key": {"apps.retries"}, "value": {"3"}}); resp.StatusCode != 303 {
		t.Fatalf("a valid typed value was refused: %d", resp.StatusCode)
	}
	if got := cfg.Fleet().Devices["lt-1"].Settings["apps.retries"]; got == "3" {
		t.Error("the value was stored as a string; the generator types its inputs and would fail at the gate")
	}
}
