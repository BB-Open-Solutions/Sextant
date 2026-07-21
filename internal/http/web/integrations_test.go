package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestIntegrationsPage(t *testing.T) {
	ts, _ := newConsole(t)
	c := client()

	resp, _ := c.Get(ts.URL + "/integrations")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	s := string(body)
	if resp.StatusCode != 200 {
		t.Fatalf("integrations = %d", resp.StatusCode)
	}
	// The seed catalog publishes netbird.setupKey, so NetBird is available and
	// its secret option is offered; Wazuh has no published options.
	if !strings.Contains(s, "NetBird VPN") || !strings.Contains(s, "netbird.setupKey") {
		t.Fatalf("NetBird integration not surfaced\n%s", s)
	}
	if !strings.Contains(s, "Wazuh SIEM") || !strings.Contains(s, "not published") {
		t.Fatal("Wazuh should show as not published in this overlay")
	}
}

// TestIntegrationCardSavesAsOneForm proves the per-card save: every option
// posts as v:<key> in one form, values land in one commit, and keys that
// belong to other forms (org settings like desktop) are untouched - the
// batch handler only diffs keys present in the form.
func TestIntegrationCardSavesAsOneForm(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	// The card renders the toggle slider and v:-named controls.
	resp, _ := c.Get(ts.URL + "/integrations")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	page := string(body)
	if !strings.Contains(page, `name="v:netbird.enable"`) {
		t.Fatal("toggle not rendered as v: radio")
	}
	if !strings.Contains(page, `name="v:netbird.managementUrl"`) {
		t.Fatal("url field not rendered as v: input")
	}

	// One save for the whole card.
	resp, err := c.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"},
		"v:netbird.enable":        {"true"},
		"v:netbird.managementUrl": {"https://netbird.bb-open.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("card save = %d", resp.StatusCode)
	}
	own, _, _ := cfg.Fleet().ScopeSettings("org")
	if own["netbird.enable"] != true || own["netbird.managementUrl"] != "https://netbird.bb-open.com" {
		t.Fatalf("card values not saved: %v", own)
	}
	// The seed org sets desktop; a card save must never clear it.
	if own["desktop"] != "plasma" {
		t.Fatalf("unrelated org setting clobbered: %v", own)
	}
}

// TestIntegrationScopeSelector: the integrations page configures at a chosen
// scope - a group first, then widen. Group saves land on the group, org
// settings stay untouched, and the toggle offers the inherit state there.
func TestIntegrationScopeSelector(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	resp, _ := c.Get(ts.URL + "/integrations?scope=group:pilot")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("group scope = %d", resp.StatusCode)
	}
	page := string(body)
	if !strings.Contains(page, `value="group:pilot" selected`) {
		t.Fatal("scope selector does not show the group")
	}
	if !strings.Contains(page, `id="ii-netbird-enable"`) {
		t.Fatal("group scope should offer the inherit state on toggles")
	}

	resp, err := c.PostForm(ts.URL+"/settings", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"group:pilot"},
		"v:netbird.enable": {"true"},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("group save = %d", resp.StatusCode)
	}
	own, _, _ := cfg.Fleet().ScopeSettings("group:pilot")
	if own["netbird.enable"] != true {
		t.Fatalf("group value not saved: %v", own)
	}
	orgOwn, _, _ := cfg.Fleet().ScopeSettings("org")
	if _, has := orgOwn["netbird.enable"]; has {
		t.Fatalf("org polluted by group save: %v", orgOwn)
	}

	// Unknown scope 404s.
	resp, _ = c.Get(ts.URL + "/integrations?scope=group:ghost")
	resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Fatalf("ghost scope = %d", resp.StatusCode)
	}
}
