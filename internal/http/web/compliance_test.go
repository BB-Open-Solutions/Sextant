package web_test

import (
	"io"
	"strings"
	"testing"
)

// The compliance page renders for a fleet without an incident store (every
// active device reads as to-spec), lists devices with their groups, and the
// policy-exposure section names the seeded policies.
func TestCompliancePageRenders(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().Get(ts.URL + "/compliance")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d\n%s", resp.StatusCode, body)
	}
	page := string(body)
	for _, want := range []string{"lt-1", "Compliance", "Policy exposure"} {
		if !strings.Contains(page, want) {
			t.Errorf("page missing %q", want)
		}
	}
	// Severity filter narrows: with no incidents, ?status=critical is empty.
	resp2, _ := client().Get(ts.URL + "/compliance?status=critical")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	// The acceptances section legitimately names device scopes on every
	// render; only the device ROWS (with their device links) must be gone.
	if strings.Contains(string(body2), `href="/devices/lt-1"`) {
		t.Error("critical filter still lists a to-spec device")
	}
}

// A device's groups read as plain text. As pills their own padding wrapped a
// name like "bb-laptops" onto a second line in this narrow column, which is a
// lot of ceremony for a word.
func TestComplianceGroupsAreNotPills(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/compliance")
	if !strings.Contains(page, "pilot") {
		t.Fatal("the device's group is not on the page at all")
	}
	if strings.Contains(page, `<span class="tag">pilot</span>`) {
		t.Error("group still rendered as a pill")
	}
}
