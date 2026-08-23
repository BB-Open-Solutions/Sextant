package web_test

import (
	"io"
	"strings"
	"testing"
)

// An integration's options must appear in exactly one place. The Integrations
// page owns them; the general Settings editor hides them, so a key is
// configured once and never argued over between two screens.
//
// Both halves are asserted here because they come from the same prefix. A
// prefix that does not match the catalog leaves the options in Settings and
// the card empty, and either check alone would still pass in one of the two
// ways that can go wrong.
func TestOsqueryOptionsLiveOnTheIntegrationsPageAndNowhereElse(t *testing.T) {
	ts, _ := newConsole(t)
	c := client()

	get := func(path string) string {
		t.Helper()
		resp, err := c.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Fatalf("%s = %d", path, resp.StatusCode)
		}
		return string(body)
	}

	integrations := get("/integrations")
	if !strings.Contains(integrations, "Fleet inventory (osquery)") {
		t.Error("the osquery card is missing from the Integrations page")
	}
	for _, key := range []string{"osquery.enable", "osquery.server"} {
		if !strings.Contains(integrations, key) {
			t.Errorf("%s is not offered on the Integrations page, so nobody can turn it on", key)
		}
	}

	settings := get("/settings")
	if strings.Contains(settings, "osquery.enable") {
		t.Error("osquery.enable is also in the Settings editor; one key, two screens, " +
			"and an operator cannot tell which one wins")
	}
}
