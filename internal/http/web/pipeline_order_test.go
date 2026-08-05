package web

import (
	"testing"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/change"
)

// TestNewestFirstOrdersTheReviewQueue: the store lists changes in filename
// order. For the upstream watcher's ids that is a hex prefix, so the queue
// appeared sorted while carrying no meaning at all - four core updates on the
// production board on 2026-08-05, in an order that told an operator nothing
// about which one was current.
func TestNewestFirstOrdersTheReviewQueue(t *testing.T) {
	at := func(min int) time.Time { return time.Date(2026, 8, 5, 11, min, 0, 0, time.UTC) }
	// Deliberately in hex-prefix order, which is what the store hands over and
	// which has nothing to do with age.
	list := []change.CR{
		{ID: "core-3eb6560edce1", Created: at(35)},
		{ID: "core-41d6ad18ac99", Created: at(65)},
		{ID: "core-46641779a4cb", Created: at(5)},
		{ID: "core-b3e94c1bab4d", Created: at(95)},
	}
	newestFirst(list)

	want := []string{"core-b3e94c1bab4d", "core-41d6ad18ac99", "core-3eb6560edce1", "core-46641779a4cb"}
	for i, id := range want {
		if list[i].ID != id {
			t.Fatalf("position %d = %s, want %s (whole order: %v)", i, list[i].ID, id, ids(list))
		}
	}
}

// TestNewestFirstHandlesEmptyAndSingle: the queues are frequently empty, and
// sorting must not be where that breaks.
func TestNewestFirstHandlesEmptyAndSingle(t *testing.T) {
	var empty []change.CR
	one := []change.CR{{ID: "solo"}}
	newestFirst(empty, one, nil)
	if len(one) != 1 || one[0].ID != "solo" {
		t.Fatalf("single-element queue disturbed: %v", ids(one))
	}
}

func ids(list []change.CR) []string {
	out := make([]string, 0, len(list))
	for _, cr := range list {
		out = append(out, cr.ID)
	}
	return out
}

// TestUpdatesURLNamesWhatIsStillRunning: a submit or merge that outran the
// grace window keeps running while the browser is already back on the board,
// where the change still reads Draft or Ready. Without this the page looks
// untouched - the "did my click do anything" problem design 0011 named for
// imaging - and the second click is how a change gets submitted twice.
func TestUpdatesURLNamesWhatIsStillRunning(t *testing.T) {
	if got := updatesURL(false, "core-41d6ad18ac99"); got != "/updates" {
		t.Fatalf("a write that finished inline should not claim to be pending: %q", got)
	}
	got := updatesURL(true, "core-41d6ad18ac99")
	if got != "/updates?pending=core-41d6ad18ac99" {
		t.Fatalf("detached write = %q", got)
	}
}

// TestUpdatesURLEscapes: change ids are operator-typed slugs. The pattern
// constrains them, but a value that reaches a URL is escaped where it is
// built, not where somebody remembers to.
func TestUpdatesURLEscapes(t *testing.T) {
	if got := updatesURL(true, "a b&c=d"); got != "/updates?pending=a+b%26c%3Dd" {
		t.Fatalf("unescaped id reached the query: %q", got)
	}
}
