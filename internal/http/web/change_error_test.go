package web_test

import (
	"context"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// A rejection is a paragraph: the command, its arguments, the remote it spoke
// to, a trace. Printing all of it on the review queue made the queue
// unreadable and still buried the line that names the cause, so the row keeps
// the first line and the rest sits behind a disclosure.
//
// The same text also passes through a credential scrub. Git quotes the remote
// it was talking to, and a remote can carry its credential in the URL - the
// console never writes one that way, but an overlay repo or a stray
// `git remote set-url` can, and then it is in the store and on the page.
func TestFailedChangeShowsHeadlineAndHidesTheRest(t *testing.T) {
	detail := strings.Join([]string{
		"gate-runner error (status 500): staging candidate failed",
		"git [fetch --quiet --force origin cr/x:refs/gate/candidate]",
		"remote: https://release-bot:ghp_secret123@forge.example.com/bb-open/overlay.git",
		"fatal: couldn't find remote ref cr/x",
	}, "\n")
	// Rejects only once armed: an edit validates too, and this test needs a
	// change that carries a commit before the submit is refused.
	var armed atomic.Bool
	reject := ports.GateFunc(func(context.Context, string, []string) error {
		if !armed.Load() {
			return nil
		}
		return &ports.ValidationError{Detail: detail}
	})
	ts, _, changes := newChangeConsoleWithGate(t, reject)

	if _, err := changes.Open(context.Background(), "cr-err", "A change that will fail", testAuthor); err != nil {
		t.Fatalf("opening the change: %v", err)
	}
	// The change needs a commit, or it is refused before the gate is reached
	// (see TestSubmitRefusesAnEmptyChange). This test is about what the page
	// does with a gate REJECTION, so give the gate something to reject.
	if err := changes.Edit(context.Background(), "cr-err",
		fleet.SetScopeSetting("device:t-1", "apps.office", true),
		"edit", ports.Author{}, "t-1"); err != nil {
		t.Fatalf("editing the change: %v", err)
	}
	armed.Store(true)
	// The submit is refused by the gate; that is the point.
	postForm(t, ts, "/changes/cr-err/submit", url.Values{"csrf": {"dev-csrf"}})

	_, page := getPage(t, ts, "/updates")

	if strings.Contains(page, "ghp_secret123") {
		t.Error("the credential from the remote URL reached the page")
	}
	if !strings.Contains(page, "release-bot") || !strings.Contains(page, "forge.example.com") {
		t.Error("scrubbing took the account or the host with it; the error is no longer actionable")
	}
	if !strings.Contains(page, "gate-runner error (status 500)") {
		t.Error("the headline is not on the row")
	}
	if !strings.Contains(page, "<details") {
		t.Error("the rest of the rejection has no disclosure to sit behind")
	}
	// The trailing line belongs in the disclosure, not on the row: if the row
	// carried it, the queue would still be a wall of text.
	row := page
	if i := strings.Index(page, "<details"); i > 0 {
		row = page[:i]
	}
	if strings.Contains(row, "couldn't find remote ref") {
		t.Error("the whole rejection is still printed on the row")
	}
}
