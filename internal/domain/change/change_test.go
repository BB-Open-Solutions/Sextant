package change

import (
	"encoding/json"
	"slices"
	"testing"
	"time"
)

var t0 = time.Date(2026, 7, 9, 12, 0, 0, 0, time.UTC)

func TestNew(t *testing.T) {
	cr, err := New("fix-office", "Enable office", "ada", "sub", t0)
	if err != nil {
		t.Fatal(err)
	}
	if cr.Branch != "cr/fix-office" || cr.Status != Draft || !cr.Open() {
		t.Fatalf("cr = %+v", cr)
	}
	for _, bad := range []string{"", "UPPER", "-x", "a b", "x/../y", "cr/inject"} {
		if _, err := New(bad, "t", "a", "sub", t0); err == nil {
			t.Errorf("id %q accepted", bad)
		}
	}
	if _, err := New("ok", "", "a", "sub", t0); err == nil {
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
	cr, _ := New("x", "t", "a", "sub", t0)
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

// RecordHosts accumulates a change request's blast radius across its edits,
// and GateHosts is what Submit hands the nix gate. Getting it wrong is not a
// crash: the gate validates a narrower set than the change actually touches,
// the eval passes, and the change reaches devices whose configuration was
// never evaluated. That is the injection firewall's scope, so the rules are
// pinned one by one.
func TestBlastRadiusAccumulation(t *testing.T) {
	t.Run("no edits validates everything", func(t *testing.T) {
		var c CR
		if got := c.GateHosts(); got != nil {
			t.Errorf("an untouched CR scoped the gate to %v; unknown radius must mean everything", got)
		}
	})

	t.Run("bounded edits union and deduplicate", func(t *testing.T) {
		var c CR
		c.RecordHosts([]string{"lt-1", "lt-2"})
		c.RecordHosts([]string{"lt-2", "lt-3"})
		got := c.GateHosts()
		want := []string{"lt-1", "lt-2", "lt-3"}
		if !slices.Equal(got, want) {
			t.Errorf("GateHosts = %v, want %v", got, want)
		}
		if c.WholeFleet {
			t.Error("bounded edits marked the change whole-fleet")
		}
	})

	t.Run("an unbounded edit widens to the whole fleet", func(t *testing.T) {
		var c CR
		c.RecordHosts([]string{"lt-1"})
		c.RecordHosts(nil) // an org-wide setting: radius is everything
		if !c.WholeFleet {
			t.Fatal("an empty host list did not mark the change whole-fleet")
		}
		if got := c.GateHosts(); got != nil {
			t.Errorf("GateHosts = %v after an org-wide edit, want nil", got)
		}
	})

	// The one that matters most. Once a change has touched everything, a
	// later narrow edit must not talk the gate back down to that edit's few
	// hosts - the earlier org-wide edit is still in the change.
	t.Run("whole-fleet is sticky across later narrow edits", func(t *testing.T) {
		var c CR
		c.RecordHosts(nil)
		c.RecordHosts([]string{"lt-1"})
		if !c.WholeFleet {
			t.Fatal("a narrow edit cleared the whole-fleet mark")
		}
		if got := c.GateHosts(); got != nil {
			t.Errorf("GateHosts = %v; a narrow edit after an org-wide one "+
				"narrowed the gate, so the org-wide change would reach "+
				"unevaluated devices", got)
		}
	})

	// A stored CR can carry "hosts": [] - a non-nil empty slice, which no
	// amount of appending produces but JSON decoding does. GateHosts
	// documents nil as "everything", so it must return the sentinel rather
	// than the empty slice it happens to be holding.
	//
	// Today gateScope tests len(recorded) > 0 and so cannot tell them apart,
	// which is why dropping the len(c.Hosts) == 0 clause survives every other
	// assertion here. That makes this the only thing standing between the
	// documented contract and a future caller that checks == nil and gates
	// nothing at all.
	t.Run("a decoded empty host list returns the everything sentinel", func(t *testing.T) {
		var c CR
		if err := json.Unmarshal([]byte(`{"hosts":[]}`), &c); err != nil {
			t.Fatal(err)
		}
		if c.Hosts == nil {
			t.Skip("the decoder produced nil, so there is nothing to distinguish")
		}
		if got := c.GateHosts(); got != nil {
			t.Errorf("GateHosts = %#v, want nil: nil is the documented "+
				"\"validate everything\" signal", got)
		}
	})

	// An empty slice is the same statement as nil: "this edit had no bounded
	// radius". They arrive from different call sites and must not diverge.
	t.Run("an empty slice is unbounded just like nil", func(t *testing.T) {
		var c CR
		c.RecordHosts([]string{})
		if !c.WholeFleet {
			t.Error("an empty (non-nil) slice was not treated as unbounded")
		}
	})
}
