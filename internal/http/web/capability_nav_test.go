package web_test

import (
	"strings"
	"testing"
)

// A deployment without the observed plane (no Postgres) has no elevation
// approvals and no imaging flow. The pages say so - /elevation answers 404 and
// /enroll a line of plain text - but the sidebar linked both anyway, so the
// first thing an operator met was a dead end they had been invited into.
//
// Reported from a running console on 2026-08-13: "404 page not found op
// /elevation". The fix is not to make the page pretend; it is to stop
// offering the door. The test console has no observed plane, which is exactly
// the deployment this is about.
func TestSidebarHidesWhatThisDeploymentCannotDo(t *testing.T) {
	ts, _ := newConsole(t)
	_, page := getPage(t, ts, "/")

	for _, dead := range []string{`href="/elevation"`, `href="/enroll"`} {
		if strings.Contains(page, dead) {
			t.Errorf("sidebar still offers %s without the store behind it", dead)
		}
	}
	// The rest of the fleet section is unaffected: hiding must be narrow, or
	// the next reader concludes the console is broken instead of unconfigured.
	for _, alive := range []string{`href="/devices"`, `href="/groups"`, `href="/settings"`, `href="/policies"`} {
		if !strings.Contains(page, alive) {
			t.Errorf("sidebar lost %s, which needs no observed plane", alive)
		}
	}
}
