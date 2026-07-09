package change

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	cr, err := New("fix-office", "Enable office", "ada", t0)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Branch != "cr/fix-office" || cr.Status != Draft || !cr.Open() {
		t.Fatalf("cr = %+v", cr)
	}
	for _, bad := range []string{"", "UPPER", "-x", "a b", "x/../y", "cr/inject"} {
		if _, err := New(bad, "t", "a", t0); err == nil {
			t.Errorf("id %q accepted", bad)
		}
	}
	if _, err := New("ok", "", "a", t0); err == nil {
		t.Error("empty title accepted")
	}
}

func TestTransitions(t *testing.T) {
	allowed := []struct{ from, to Status }{
		{Draft, Building}, {Draft, Abandoned},
		{Building, Ready}, {Building, Failed},
		{Failed, Draft}, {Failed, Building}, {Failed, Abandoned},
		{Ready, Merged}, {Ready, Draft}, {Ready, Abandoned},
	}
	for _, tc := range allowed {
		if !CanTransition(tc.from, tc.to) {
			t.Errorf("%s -> %s should be allowed", tc.from, tc.to)
		}
	}
	denied := []struct{ from, to Status }{
		{Draft, Merged}, {Draft, Ready}, {Draft, Failed},
		{Building, Merged}, {Building, Draft}, {Building, Abandoned},
		{Merged, Draft}, {Merged, Abandoned},
		{Abandoned, Draft}, {Abandoned, Merged},
		{Failed, Ready}, {Failed, Merged},
	}
	for _, tc := range denied {
		if CanTransition(tc.from, tc.to) {
			t.Errorf("%s -> %s must be denied", tc.from, tc.to)
		}
	}
}

func TestTransitionUpdatesCR(t *testing.T) {
	cr, _ := New("x", "t", "a", t0)
	later := t0.Add(time.Hour)
	if err := cr.Transition(Building, later); err != nil {
		t.Fatal(err)
	}
	if cr.Status != Building || !cr.Updated.Equal(later) {
		t.Fatalf("cr = %+v", cr)
	}
	if err := cr.Transition(Merged, later); err == nil {
		t.Fatal("building -> merged accepted")
	}
	if err := cr.Transition("weird", later); err == nil {
		t.Fatal("unknown status accepted")
	}
	// Terminal states stay terminal.
	cr.Status = Merged
	if err := cr.Transition(Draft, later); err == nil {
		t.Fatal("merged -> draft accepted")
	}
}
