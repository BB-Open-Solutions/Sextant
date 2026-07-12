package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestSecretsPageRegisterAndRemove(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	// Empty registry renders with the register form (dev session is owner).
	resp, _ := c.Get(ts.URL + "/secrets")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "No secret references registered") {
		t.Fatalf("secrets page = %d\n%s", resp.StatusCode, body)
	}

	// Register a reference (name only).
	resp, _ = c.PostForm(ts.URL+"/secrets", url.Values{
		"csrf": {"dev-csrf"}, "name": {"netbird-setupkey"}, "description": {"join key"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("register = %d, want 303", resp.StatusCode)
	}
	if !cfg.Fleet().HasSecretRef("netbird-setupkey") {
		t.Fatal("secret reference not committed")
	}

	// It now appears on the page.
	resp, _ = c.Get(ts.URL + "/secrets")
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "netbird-setupkey") {
		t.Fatal("registered reference not listed")
	}

	// Remove it.
	resp, _ = c.PostForm(ts.URL+"/secrets/netbird-setupkey/remove", url.Values{"csrf": {"dev-csrf"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("remove = %d, want 303", resp.StatusCode)
	}
	if cfg.Fleet().HasSecretRef("netbird-setupkey") {
		t.Fatal("secret reference still present after remove")
	}
}

func TestSecretSettingRequiresRegisteredReference(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	set := func(value string) int {
		resp, _ := c.PostForm(ts.URL+"/settings", url.Values{
			"csrf": {"dev-csrf"}, "scope": {"org"}, "key": {"netbird.setupKey"},
			"action": {"set"}, "value": {value}})
		resp.Body.Close()
		return resp.StatusCode
	}

	// A raw secret value is rejected outright (never reaches git).
	if code := set("nb_live_the_actual_key=="); code != 400 {
		t.Fatalf("raw secret value = %d, want 400", code)
	}
	// A reference name that is not registered is rejected.
	if code := set("netbird-setupkey"); code != 400 {
		t.Fatalf("unregistered reference = %d, want 400", code)
	}
	if _, ok := cfg.Fleet().Org.Settings["netbird.setupKey"]; ok {
		t.Fatal("a secret setting was written without a registered reference")
	}

	// Register the reference, then the secret setting is accepted - and stores
	// the NAME, never a value.
	resp, _ := c.PostForm(ts.URL+"/secrets", url.Values{"csrf": {"dev-csrf"}, "name": {"netbird-setupkey"}})
	resp.Body.Close()
	if code := set("netbird-setupkey"); code != 303 {
		t.Fatalf("registered reference = %d, want 303", code)
	}
	if got := cfg.Fleet().Org.Settings["netbird.setupKey"]; got != "netbird-setupkey" {
		t.Fatalf("secret setting stored %v, want the reference name", got)
	}
}
