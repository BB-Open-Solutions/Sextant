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

// Hiding the link is not the whole answer: a bookmark, a notification link or
// a typed URL still arrives. Those pages answered with http.NotFound or a
// line of plain text - a blank white page with no frame, no explanation and
// no way back, and in one case a 404 claiming the page does not exist when
// the truth is that this deployment has no database behind it.
func TestPagesThatNeedTheStoreExplainThemselves(t *testing.T) {
	ts, _ := newConsole(t)
	for _, path := range []string{"/elevation", "/enroll"} {
		code, page := getPage(t, ts, path)
		if code != 503 {
			t.Errorf("%s answered %d; a missing dependency is 503, not 404 or 200", path, code)
		}
		if !strings.Contains(page, "PostgreSQL") {
			t.Errorf("%s does not say what is missing", path)
		}
		// The console's own frame, so the reader is still somewhere.
		if !strings.Contains(page, `href="/settings"`) {
			t.Errorf("%s renders outside the console layout", path)
		}
		// And it says what still works, so nobody concludes the console is down.
		if !strings.Contains(page, "policies") {
			t.Errorf("%s does not say what is unaffected", path)
		}
	}
}
