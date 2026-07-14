package web_test

import (
	"io"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/http/web"
)

// TestWizardProgressBarIsCSPSafe guards a regression: the console's global
// CSP (default-src 'self', no 'unsafe-inline') silently zeroes out any
// style="width:N%" progress bar - it never errors, the bar just always
// renders at 0 width. The fix computes a bucketed width CLASS in Go
// (barW/bar-w-N, same pattern as the rollout/pipeline convergence bars)
// instead of an inline style attribute. Reuses newWizardConsole's seeded
// imaging job at 68% progress.
func TestWizardProgressBarIsCSPSafe(t *testing.T) {
	ts := newWizardConsole(t, web.DevSessions{})
	c := client()

	resp, _ := c.Get(ts.URL + "/enroll/nuc-1/wizard")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("wizard = %d", resp.StatusCode)
	}
	s := string(body)

	if strings.Contains(s, `style="width`) {
		t.Error("progress bar still uses an inline style=, which the CSP silently disables")
	}
	// 68% buckets to the nearest 5 -> bar-w-70 (barBucket rounds up at the
	// midpoint: (68+2)/5*5 = 70).
	if !strings.Contains(s, "bar-w-70") {
		t.Error("progress bar missing the CSP-safe bar-w-N width class")
	}
}
