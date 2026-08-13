package web_test

import (
	"io"
	"net/url"
	"strings"
	"testing"
)

// The policies export streams every policy (assigned or not) with its
// assignments, controls and coverage - the audit artifact.
func TestPoliciesCSVExport(t *testing.T) {
	ts, _ := newConsole(t)
	// Seed one hand-made policy with a control annotation (no assignment:
	// dormant rules must still export).
	if code := postForm(t, ts, "/policies", url.Values{
		"csrf": {"dev-csrf"}, "id": {"audit-base"},
		"description": {"audit baseline"}, "settings": {"desktop = gnome"},
		"controls": {"BIO 12.3.1, ISO 27002 8.9"}}); code != 303 {
		t.Fatalf("seed policy status = %d", code)
	}
	resp, err := client().Get(ts.URL + "/policies.csv")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/csv") {
		t.Errorf("content-type = %q", ct)
	}
	csv := string(body)
	if !strings.HasPrefix(csv, "policy,description,controls,profile,state,settings,enforced_keys,target,filter,devices_reached,devices_behind") {
		t.Errorf("missing header row:\n%s", csv)
	}
	// The unassigned policy exports with its controls and empty coverage.
	if !strings.Contains(csv, "audit-base") || !strings.Contains(csv, "BIO 12.3.1; ISO 27002 8.9") {
		t.Errorf("policy row with controls missing:\n%s", csv)
	}
}
