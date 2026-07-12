package web_test

import (
	"net/url"
	"testing"
)

func TestRiskAcceptanceRecordAndWithdraw(t *testing.T) {
	ts, cfg := newConsole(t)
	c := client()

	accept := func(key, reason string) int {
		resp, _ := c.PostForm(ts.URL+"/acceptances", url.Values{
			"csrf": {"dev-csrf"}, "scope": {"org"}, "key": {key}, "reason": {reason}})
		resp.Body.Close()
		return resp.StatusCode
	}

	// A justification is mandatory (comply-or-explain).
	if code := accept("secureboot.enable", ""); code != 400 {
		t.Fatalf("empty justification = %d, want 400", code)
	}
	// A documented acceptance is recorded at the scope.
	if code := accept("secureboot.enable", "kiosk without a TPM; risk owned by X"); code != 303 {
		t.Fatalf("accept = %d, want 303", code)
	}
	if got := cfg.Fleet().Org.Accepted["secureboot.enable"]; got == "" {
		t.Fatal("acceptance not committed at org scope")
	}

	// Withdraw removes it.
	resp, _ := c.PostForm(ts.URL+"/acceptances/clear", url.Values{
		"csrf": {"dev-csrf"}, "scope": {"org"}, "key": {"secureboot.enable"}})
	resp.Body.Close()
	if resp.StatusCode != 303 {
		t.Fatalf("withdraw = %d, want 303", resp.StatusCode)
	}
	if _, ok := cfg.Fleet().Org.Accepted["secureboot.enable"]; ok {
		t.Fatal("acceptance still present after withdraw")
	}
}
