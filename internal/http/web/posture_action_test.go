package web_test

import (
	"net/http"
	"net/url"
	"testing"
)

// posture action buttons write the two documented keys at device scope.
func TestPostureActionApplies(t *testing.T) {
	ts, cfg := newConsole(t)
	post := func(action string) *http.Response {
		t.Helper()
		resp, err := client().PostForm(ts.URL+"/devices/lt-1/posture",
			url.Values{"csrf": {"dev-csrf"}, "action": {action}})
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp
	}

	if resp := post("enable-sb"); resp.StatusCode != 303 {
		t.Fatalf("enable-sb = %d", resp.StatusCode)
	}
	own, _, _ := cfg.Fleet().ScopeSettings("device:lt-1")
	if own["secureboot.enable"] != true {
		t.Fatalf("secureboot not enabled: %v", own)
	}
	if resp := post("disable-sb"); resp.StatusCode != 303 {
		t.Fatalf("disable-sb = %d", resp.StatusCode)
	}
	own, _, _ = cfg.Fleet().ScopeSettings("device:lt-1")
	if own["secureboot.enable"] != false {
		t.Fatalf("secureboot not disabled: %v", own)
	}
	// Only the whitelisted actions; anything else 400s.
	if resp := post("rm-rf"); resp.StatusCode != 400 {
		t.Fatalf("bogus action = %d, want 400", resp.StatusCode)
	}
}
