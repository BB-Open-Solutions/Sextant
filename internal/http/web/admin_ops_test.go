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
	// Unchecking an enabled protection is weakening (fix E): the bare POST
	// renders a confirmation instead of saving.
	r3, _ := client().PostForm(ts.URL+"/assurance", url.Values{"csrf": {"dev-csrf"}})
	body3, _ := io.ReadAll(r3.Body)
	r3.Body.Close()
	if r3.StatusCode != 200 || !strings.Contains(string(body3), "Confirm governance change") || !strings.Contains(string(body3), "Four-eyes") {
		t.Fatalf("assurance weaken preview = %d, want a confirmation page naming Four-eyes", r3.StatusCode)
	}
	if a := cfg.Fleet().Assurance; a == nil || !a.RequireFourEyes {
		t.Fatalf("unconfirmed uncheck must not save: %+v", cfg.Fleet().Assurance)
	}
	// confirmed=1 actually saves the weakened state.
	r4, _ := client().PostForm(ts.URL+"/assurance", url.Values{"csrf": {"dev-csrf"}, "confirmed": {"1"}})
	r4.Body.Close()
	if a := cfg.Fleet().Assurance; a == nil || a.RequireFourEyes {
		t.Fatalf("assurance off = %+v", cfg.Fleet().Assurance)
	}
}

// TestAssuranceConfirmationOnlyOnWeakening proves fix E's guard is precise:
// enabling a protection, and a save that changes nothing, both go straight
// through without a confirmation detour. Only turning an enabled protection
// OFF triggers one.
func TestAssuranceConfirmationOnlyOnWeakening(t *testing.T) {
	ts, cfg := newConsole(t)

	// Starting from nothing enabled, turning two protections ON saves
	// directly - there is nothing to weaken yet.
	if code := postForm(t, ts, "/assurance", url.Values{
		"requireChangeRequest": {"1"}, "requireTestWave": {"1"},
	}); code != 303 {
		t.Fatalf("enable = %d, want a direct save", code)
	}
	a := cfg.Fleet().Assurance
	if a == nil || !a.RequireChangeRequest || !a.RequireTestWave {
		t.Fatalf("assurance = %+v", a)
	}

	// Resubmitting the exact same state changes nothing: also a direct save.
	if code := postForm(t, ts, "/assurance", url.Values{
		"requireChangeRequest": {"1"}, "requireTestWave": {"1"},
	}); code != 303 {
		t.Fatalf("no-op resave = %d, want a direct save", code)
	}

	// Turning ONE of the two off, while leaving the other on, is weakening -
	// even though the form also (redundantly) re-asserts the one staying on.
	code, page := postFormBody(t, ts, "/assurance", url.Values{"requireTestWave": {"1"}})
	if code != 200 || !strings.Contains(page, "Confirm governance change") {
		t.Fatalf("partial uncheck = %d, want a confirmation page", code)
	}
	if !strings.Contains(page, "change-request") && !strings.Contains(page, "Require change-request") {
		t.Errorf("confirmation does not name the protection being removed: %s", page)
	}
	if a := cfg.Fleet().Assurance; a == nil || !a.RequireChangeRequest {
		t.Fatalf("unconfirmed uncheck must not save: %+v", a)
	}
}
