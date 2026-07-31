package elevation

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 31, 10, 0, 0, 0, time.UTC)

func pending() Request {
	return Request{ID: "e1", Tag: "lt-1", User: "bbuijs", State: Pending, Created: t0}
}

// The default has to be refusal. Every other property here is secondary to
// this one: if an unanswered request granted anything, the whole feature would
// be a way to get administrator rights by waiting.
func TestAnUnansweredRequestNeverGrants(t *testing.T) {
	r := pending()
	for _, at := range []time.Duration{0, time.Second, TTL - time.Second, TTL, time.Hour} {
		if r.Grants(t0.Add(at)) {
			t.Errorf("a pending request granted the elevation after %s", at)
		}
	}
	if got := r.Resolve(t0.Add(TTL)); got != Expired {
		t.Errorf("state after the window is %q, want %q", got, Expired)
	}
}

func TestApprovalGrantsInsideTheWindow(t *testing.T) {
	r, err := pending().Decide(true, "beheerder", t0.Add(30*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if !r.Grants(t0.Add(time.Minute)) {
		t.Fatal("an approval given inside the window does not grant")
	}
	if r.DecidedBy != "beheerder" {
		t.Errorf("DecidedBy = %q; an approval must record who gave it", r.DecidedBy)
	}
}

// The one that is easy to get wrong. An administrator who approves after the
// window closed must not authorise the NEXT thing that user tries: the user
// gave up minutes ago, and a stale approval would sail an unrelated action
// through with nobody asked.
func TestAnApprovalDoesNotOutliveItsRequest(t *testing.T) {
	r := pending()
	r.State, r.Decided, r.DecidedBy = Approved, t0.Add(2*TTL), "beheerder"
	if r.Grants(t0.Add(2 * TTL)) {
		t.Fatal("an approval given after the window still granted the elevation")
	}
	// And an approval given in time does not keep granting forever either.
	ok, err := pending().Decide(true, "beheerder", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if ok.Grants(t0.Add(TTL + time.Second)) {
		t.Fatal("an approval still granted after its request had expired")
	}
}

func TestDenialIsFinalAndDistinctFromExpiry(t *testing.T) {
	r, err := pending().Decide(false, "beheerder", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if r.State != Denied {
		t.Fatalf("state = %q, want %q", r.State, Denied)
	}
	// A denial stays a denial even long after the window; it must never age
	// into Expired, because "we said no" and "nobody was there" are different
	// facts and an auditor reads them differently.
	if got := r.Resolve(t0.Add(time.Hour)); got != Denied {
		t.Errorf("a denial became %q after the window", got)
	}
}

// Two administrators clicking at the same moment must not produce two
// verdicts, and a late click must not revive a request that has expired.
func TestARequestIsAnsweredOnlyOnce(t *testing.T) {
	first, err := pending().Decide(true, "a", t0.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := first.Decide(false, "b", t0.Add(2*time.Second)); err == nil {
		t.Error("a second decision was accepted on an already-approved request")
	}
	if _, err := pending().Decide(true, "a", t0.Add(TTL)); err == nil {
		t.Error("an expired request accepted a decision")
	}
}

func TestADecisionMustNameItsApprover(t *testing.T) {
	if _, err := pending().Decide(true, "  ", t0.Add(time.Second)); err == nil {
		t.Fatal("an approval with no approver was accepted; the audit trail would name nobody")
	}
}

func TestValidRefusesAnIncompleteRequest(t *testing.T) {
	for name, r := range map[string]Request{
		"no id":     {Tag: "lt-1", User: "u", Created: t0},
		"no device": {ID: "e1", User: "u", Created: t0},
		"no user":   {ID: "e1", Tag: "lt-1", Created: t0},
		"no time":   {ID: "e1", Tag: "lt-1", User: "u"},
	} {
		if err := r.Valid(); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	if err := pending().Valid(); err != nil {
		t.Errorf("a complete request was refused: %v", err)
	}
}

// Waited drives what the approver reads. It must stop counting once answered,
// and must not run past the window for one nobody answered - a queue showing
// "waiting 3 days" for requests that died in five minutes is noise.
func TestWaitedStopsAtTheDecisionAndAtTheWindow(t *testing.T) {
	r, _ := pending().Decide(true, "beheerder", t0.Add(20*time.Second))
	if got := r.Waited(t0.Add(time.Hour)); got != 20*time.Second {
		t.Errorf("decided request waited %s, want 20s", got)
	}
	if got := pending().Waited(t0.Add(time.Hour)); got != TTL {
		t.Errorf("unanswered request waited %s, want the window %s", got, TTL)
	}
}
