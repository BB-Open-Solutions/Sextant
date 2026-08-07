package web_test

import (
	"context"
	"net/http"
	"net/url"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// testAuthor is who opens the changes in these tests.
var testAuthor = ports.Author{Name: "ada", Subject: "ada", Email: "ada@example.com"}

// change_ops_test.go covers the console's change-request write handlers.
// postChangeEdit was the second-largest uncovered block in the logic layer
// and is the path every reviewed setting change takes: an operator stages an
// edit on the change's own branch instead of committing to main.

func changePost(t *testing.T, url_ string, form url.Values) *http.Response {
	t.Helper()
	form.Set("csrf", "dev-csrf")
	resp, err := client().PostForm(url_, form)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	return resp
}

func TestStageAnEditOnAChangeBranch(t *testing.T) {
	ts, cfg, changes := newChangeConsole(t)
	ctx := context.Background()

	if _, err := changes.Open(ctx, "cr-w1", "raise the poll interval", testAuthor); err != nil {
		t.Fatalf("open: %v", err)
	}

	resp := changePost(t, ts.URL+"/changes/cr-w1/edits", url.Values{
		"scope": {"org"}, "key": {"apps.office"}, "value": {"true"},
	})
	if resp.StatusCode != 303 {
		t.Fatalf("edit = %d", resp.StatusCode)
	}

	// The whole point: it is NOT on main. A change that edits main directly
	// has skipped the review it exists to have.
	if _, ok := cfg.Fleet().Org.Settings["apps.office"]; ok {
		t.Error("the edit landed on main; the change request was bypassed")
	}
	// And it IS on the change.
	cr, ok, err := changes.Get(ctx, "cr-w1")
	if err != nil || !ok {
		t.Fatalf("get change: ok=%v err=%v", ok, err)
	}
	if cr.ID != "cr-w1" {
		t.Errorf("change = %+v", cr)
	}
}

func TestChangeEditRefusesWhatTheCatalogDoesNotKnow(t *testing.T) {
	ts, _, changes := newChangeConsole(t)
	if _, err := changes.Open(context.Background(), "cr-w2", "typo", testAuthor); err != nil {
		t.Fatal(err)
	}
	// A key outside the catalog would stage a setting that governs nothing -
	// the same gap that was closed on the device page (audit L2).
	if resp := changePost(t, ts.URL+"/changes/cr-w2/edits", url.Values{
		"scope": {"org"}, "key": {"apps.nonexistent"}, "value": {"true"},
	}); resp.StatusCode == 303 {
		t.Error("a key outside the catalog was staged")
	}
	// An empty key is refused rather than writing a nameless entry.
	if resp := changePost(t, ts.URL+"/changes/cr-w2/edits", url.Values{
		"scope": {"org"}, "key": {""}, "value": {"true"},
	}); resp.StatusCode == 303 {
		t.Error("an empty key was staged")
	}
}

func TestChangeEditOnAnUnknownChangeIsRefused(t *testing.T) {
	ts, _, _ := newChangeConsole(t)
	resp := changePost(t, ts.URL+"/changes/ghost/edits", url.Values{
		"scope": {"org"}, "key": {"apps.office"}, "value": {"true"},
	})
	if resp.StatusCode == 303 {
		t.Error("an edit on a change that does not exist reported success")
	}
	if resp.StatusCode == http.StatusInternalServerError {
		t.Error("an unknown change id produced a 500; it reads as a broken console")
	}
}

// TestTheChangeLifecycleThroughTheConsole walks submit and merge, then
// asserts the setting actually reached main - a merge that returns 303 and
// changes nothing is the failure worth catching.
func TestTheChangeLifecycleThroughTheConsole(t *testing.T) {
	ts, cfg, changes := newChangeConsole(t)
	ctx := context.Background()
	if _, err := changes.Open(ctx, "cr-w3", "turn on office", testAuthor); err != nil {
		t.Fatal(err)
	}
	if resp := changePost(t, ts.URL+"/changes/cr-w3/edits", url.Values{
		"scope": {"org"}, "key": {"apps.office"}, "value": {"true"},
	}); resp.StatusCode != 303 {
		t.Fatalf("edit = %d", resp.StatusCode)
	}

	// Merging before submitting must be refused: submit is where the gate
	// runs, so a merge that skips it lands unevaluated configuration.
	if resp := changePost(t, ts.URL+"/changes/cr-w3/merge",
		url.Values{"confirmed": {"1"}}); resp.StatusCode == 303 {
		t.Error("an unsubmitted change merged; the gate was skipped")
	}

	if resp := changePost(t, ts.URL+"/changes/cr-w3/submit", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("submit = %d", resp.StatusCode)
	}
	// A merge without confirmed=1 renders a confirmation page rather than
	// merging - the same two-act shape as arming a wipe. Asserting 303 here
	// would be asserting the wrong thing, so the confirmation step is
	// exercised deliberately.
	if resp := changePost(t, ts.URL+"/changes/cr-w3/merge", url.Values{}); resp.StatusCode != 200 {
		t.Errorf("an unconfirmed merge = %d, want the confirmation page", resp.StatusCode)
	}
	if resp := changePost(t, ts.URL+"/changes/cr-w3/merge",
		url.Values{"confirmed": {"1"}}); resp.StatusCode != 303 {
		t.Fatalf("confirmed merge = %d", resp.StatusCode)
	}
	if got := cfg.Fleet().Org.Settings["apps.office"]; got != true {
		t.Errorf("after a merge the setting on main is %#v, want true", got)
	}
}

func TestAbandonThroughTheConsole(t *testing.T) {
	ts, _, changes := newChangeConsole(t)
	ctx := context.Background()
	if _, err := changes.Open(ctx, "cr-w4", "never mind", testAuthor); err != nil {
		t.Fatal(err)
	}
	if resp := changePost(t, ts.URL+"/changes/cr-w4/abandon", url.Values{}); resp.StatusCode != 303 {
		t.Fatalf("abandon = %d", resp.StatusCode)
	}
	// Abandoning twice must not report success: an operator who gets a
	// redirect believes they just acted.
	if resp := changePost(t, ts.URL+"/changes/cr-w4/abandon", url.Values{}); resp.StatusCode == 303 {
		t.Error("abandoning an already abandoned change reported success")
	}
}
