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

	// Update class and assigned user.
	if resp := post("/devices/lt-1/update", url.Values{"setclass": {"1"}, "class": {"kiosk"}}); resp.StatusCode != 303 {
		t.Fatalf("set class = %d", resp.StatusCode)
	}
	if resp := post("/devices/lt-1/update", url.Values{"setuser": {"1"}, "assignedUser": {"ada"}}); resp.StatusCode != 303 {
		t.Fatalf("assign = %d", resp.StatusCode)
	}
	d := cfg.Fleet().Devices["lt-1"]
	if d.Class != "kiosk" || d.AssignedUser != "ada" {
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
