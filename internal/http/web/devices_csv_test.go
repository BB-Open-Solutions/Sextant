package web_test

import (
	"io"
	"strings"
	"testing"
)

// The CSV export streams the devices table with the baseline verdict: header
// row, one line per seeded device, attachment disposition. Seeded devices
// have no inventory status, so every active one reads "attention" with at
// least the recency criterion named.
func TestDevicesCSVExport(t *testing.T) {
	ts, _ := newConsole(t)
	resp, err := client().Get(ts.URL + "/devices.csv")
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
	if cd := resp.Header.Get("Content-Disposition"); !strings.Contains(cd, "attachment") {
		t.Errorf("content-disposition = %q", cd)
	}
	csv := string(body)
	if !strings.HasPrefix(csv, "tag,class,hardware,assigned_user,groups,online,revision,baseline,failing_criteria") {
		t.Errorf("missing header row:\n%s", csv)
	}
	if !strings.Contains(csv, "lt-1") {
		t.Errorf("missing seeded device row:\n%s", csv)
	}
	if !strings.Contains(csv, "attention") || !strings.Contains(csv, "no recent check-in") {
		t.Errorf("missing baseline verdict/criteria:\n%s", csv)
	}
	// The filter params narrow the export like the page.
	resp2, _ := client().Get(ts.URL + "/devices.csv?q=no-such-device")
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if strings.Contains(string(body2), "lt-1") {
		t.Error("filtered export still contains excluded device")
	}
}
