package web_test

import (
	"io"
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
