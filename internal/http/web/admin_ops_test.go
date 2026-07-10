package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

func TestAuditPageAndAssurance(t *testing.T) {
	ts, cfg := newConsole(t)

	// Audit trail renders the seed commit.
	resp, _ := client().Get(ts.URL + "/audit")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 || !strings.Contains(string(body), "seed") {
		t.Fatalf("audit = %d", resp.StatusCode)
	}

	// Four-eyes toggle round-trips as config-as-data.
	form := url.Values{"csrf": {"dev-csrf"}, "requireFourEyes": {"on"}}
	r2, _ := client().PostForm(ts.URL+"/assurance", form)
	r2.Body.Close()
	if r2.StatusCode != 303 {
		t.Fatalf("assurance on = %d", r2.StatusCode)
	}
	if a := cfg.Fleet().Assurance; a == nil || !a.RequireFourEyes {
		t.Fatalf("assurance = %+v", cfg.Fleet().Assurance)
	}
	r3, _ := client().PostForm(ts.URL+"/assurance", url.Values{"csrf": {"dev-csrf"}})
	r3.Body.Close()
	if a := cfg.Fleet().Assurance; a == nil || a.RequireFourEyes {
		t.Fatalf("assurance off = %+v", cfg.Fleet().Assurance)
	}
}
