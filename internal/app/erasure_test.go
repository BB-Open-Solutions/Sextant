package app

import (
	"context"
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// erasure_test.go. What is asserted here is mostly the HONESTY of the
// report, not the delete: a GDPR art. 17 answer that overstates what it
// removed is how somebody ends up telling a data subject their data is gone
// when it is not.

type memErasure struct {
	counts  ports.PersonalDataCounts
	erased  bool
	subject string
	user    string
}

func (m *memErasure) CountPersonalData(_ context.Context, _, subject, user string) (ports.PersonalDataCounts, error) {
	m.subject, m.user = subject, user
	return m.counts, nil
}

func (m *memErasure) ErasePersonalData(_ context.Context, _, subject, user string) (ports.PersonalDataCounts, error) {
	m.erased = true
	m.subject, m.user = subject, user
	return m.counts, nil
}

// TestPreviewRemovesNothing is the first half of the two-act design: an
// operator sees what would go before anything goes.
func TestPreviewRemovesNothing(t *testing.T) {
	store := &memErasure{counts: ports.PersonalDataCounts{SeenUser: 1, Notifications: 4}}
	s := NewErasureService(store, nil, "t1", nil)

	rep, err := s.Preview(context.Background(), "sub-1", "ada")
	if err != nil {
		t.Fatalf("preview: %v", err)
	}
	if store.erased {
		t.Error("a preview deleted data")
	}
	if !rep.DryRun {
		t.Error("the report does not say it was a preview")
	}
	if rep.Removed.Total() != 5 {
		t.Errorf("preview counted %d, want 5", rep.Removed.Total())
	}
}

// TestBothIdentifiersTravel: a person is an OIDC subject in the console and
// an OS username on their device, and the two do not have to match. Passing
// only one and reporting success is the failure this guards.
func TestBothIdentifiersTravel(t *testing.T) {
	store := &memErasure{}
	s := NewErasureService(store, nil, "t1", nil)
	if _, err := s.Erase(context.Background(), "sub-1", "ada", ports.Author{Name: "op"}); err != nil {
		t.Fatal(err)
	}
	if store.subject != "sub-1" || store.user != "ada" {
		t.Errorf("store got subject=%q user=%q; both must travel", store.subject, store.user)
	}
}

func TestErasureNeedsAtLeastOneIdentifier(t *testing.T) {
	s := NewErasureService(&memErasure{}, nil, "t1", nil)
	if _, err := s.Preview(context.Background(), "  ", " "); err == nil {
		t.Error("an erasure with no identifier at all was accepted")
	}
	// Blank identifiers are the dangerous case: an empty subject matches
	// every row whose subject column is empty, and group notifications have
	// exactly that shape.
	store := &memErasure{}
	s = NewErasureService(store, nil, "t1", nil)
	if _, err := s.Erase(context.Background(), "", "", ports.Author{}); err == nil {
		t.Error("a blank erasure ran")
	}
	if store.erased {
		t.Error("a blank erasure reached the store")
	}
}

// TestTheReportAlwaysNamesWhatSurvives is the point of the whole feature.
func TestTheReportAlwaysNamesWhatSurvives(t *testing.T) {
	s := NewErasureService(&memErasure{}, nil, "t1", nil)
	rep, err := s.Erase(context.Background(), "sub-1", "ada", ports.Author{Name: "op"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Remaining) == 0 {
		t.Fatal("the report claims nothing remains; the git history always does")
	}
	joined := strings.ToLower(strings.Join(rep.Remaining, " "))
	for _, want := range []string{"git history", "diagnostics"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the report does not mention %q:\n%s", want, joined)
		}
	}
}

// TestAMissingIdentifierIsCalledOut: an operator who supplies only one name
// must be told which half was not searched, rather than reading a clean
// report and believing it was complete.
func TestAMissingIdentifierIsCalledOut(t *testing.T) {
	s := NewErasureService(&memErasure{}, nil, "t1", nil)

	rep, err := s.Preview(context.Background(), "sub-1", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(rep.Remaining, " "), "device username") {
		t.Error("no device username was given and the report does not say so")
	}

	if rep, err = s.Preview(context.Background(), "", "ada"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(rep.Remaining, " "), "console subject") {
		t.Error("no console subject was given and the report does not say so")
	}
}

// TestDecisionsMadeForOthersAreLeftAndExplained: erasing a request this
// person DECIDED would destroy a different data subject's record of who
// approved their access.
func TestDecisionsMadeForOthersAreLeftAndExplained(t *testing.T) {
	store := &memErasure{counts: ports.PersonalDataCounts{Elevation: 2, ElevationDecided: 3}}
	s := NewErasureService(store, nil, "t1", nil)
	rep, err := s.Erase(context.Background(), "sub-1", "ada", ports.Author{Name: "op"})
	if err != nil {
		t.Fatal(err)
	}
	// Decisions are not counted as removed.
	if rep.Removed.Total() != 2 {
		t.Errorf("removed total = %d, want 2 (the raised requests only)", rep.Removed.Total())
	}
	joined := strings.Join(rep.Remaining, " ")
	if !strings.Contains(joined, "DECIDED") {
		t.Errorf("the surviving decisions are not explained:\n%s", joined)
	}
}
