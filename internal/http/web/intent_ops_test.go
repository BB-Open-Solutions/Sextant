package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

func TestIntentPanelArmClear(t *testing.T) {
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

	// Lock arms.
	if resp := post("/devices/lt-1/intent", url.Values{"intent": {"lock"}}); resp.StatusCode != 303 {
		t.Fatalf("lock = %d", resp.StatusCode)
	}
	if cfg.Fleet().Devices["lt-1"].Intent != "lock" {
		t.Fatal("lock not armed")
	}
	// Wipe without the typed confirmation is refused.
	if resp := post("/devices/lt-1/intent",
		url.Values{"intent": {"wipe"}, "force": {"1"}, "confirm": {"wrong"}}); resp.StatusCode != 400 {
		t.Fatalf("unconfirmed wipe = %d, want 400", resp.StatusCode)
	}
	// Correct confirmation arms wipe.
	if resp := post("/devices/lt-1/intent",
		url.Values{"intent": {"wipe"}, "force": {"1"}, "confirm": {"lt-1"}}); resp.StatusCode != 303 {
		t.Fatalf("confirmed wipe = %d", resp.StatusCode)
	}
	if cfg.Fleet().Devices["lt-1"].Intent != "wipe" {
		t.Fatal("wipe not armed")
	}
	// Clear.
	if resp := post("/devices/lt-1/intent/clear", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("clear = %d", resp.StatusCode)
	}
	if cfg.Fleet().Devices["lt-1"].Intent != "" {
		t.Fatal("intent not cleared")
	}
}
