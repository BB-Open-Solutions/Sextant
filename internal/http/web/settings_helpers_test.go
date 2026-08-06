package web

import (
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// settings_helpers_test.go covers the small pure functions behind the
// settings page. They were at 0% or barely covered, and each one shapes what
// an operator sees or what gets written back.

// TestSplitRangeOnlyAcceptsARange backs the time-range control. An input the
// splitter half-understands is worse than one it rejects: the form would
// render one populated field and one empty one, and saving that writes a
// window nobody chose.
func TestSplitRangeOnlyAcceptsARange(t *testing.T) {
	cases := []struct{ in, from, to string }{
		{"02:00-04:00", "02:00", "04:00"},
		{" 02:00 - 04:00 ", "02:00", "04:00"},
		// A window that wraps midnight is a legitimate maintenance window and
		// must survive the round trip.
		{"23:00-01:00", "23:00", "01:00"},
		// No separator at all: not a range, so nothing is claimed.
		{"02:00", "", ""},
		{"", "", ""},
		{"always", "", ""},
	}
	for _, c := range cases {
		t.Run(c.in, func(t *testing.T) {
			from, to := splitRange(c.in)
			if from != c.from || to != c.to {
				t.Errorf("splitRange(%q) = (%q, %q), want (%q, %q)", c.in, from, to, c.from, c.to)
			}
		})
	}
}

func TestValueLinesRendersAListOnePerLine(t *testing.T) {
	// The list editor is line-based, so what this produces is what the
	// operator edits. Anything but one item per line silently merges or
	// splits entries when they save.
	got := valueLines([]any{"0.pool.ntp.org", "1.pool.ntp.org"})
	if got != "0.pool.ntp.org\n1.pool.ntp.org" {
		t.Errorf("valueLines = %q", got)
	}
	// Non-strings still render, rather than vanishing: a list that came back
	// from JSON as numbers must be editable, not silently emptied.
	if got = valueLines([]any{1, true, "x"}); got != "1\ntrue\nx" {
		t.Errorf("mixed list = %q", got)
	}
	// A value that is not a list at all yields nothing, which is what the
	// template treats as "no list control here".
	for _, v := range []any{nil, "a string", 42, map[string]any{"k": "v"}} {
		if got := valueLines(v); got != "" {
			t.Errorf("valueLines(%T) = %q, want empty", v, got)
		}
	}
	if got := valueLines([]any{}); got != "" {
		t.Errorf("empty list = %q", got)
	}
}

// TestListSlotsOffersDefaultsAsPlaceholdersNotValues is the behaviour the
// function's own comment calls the point of the page: prefilling defaults as
// VALUES would mean that saving the page for any unrelated reason writes
// them explicitly at this scope, moving the row from "inherits" to "modified
// here" and destroying the provenance the page exists to show.
func TestListSlotsOffersDefaultsAsPlaceholdersNotValues(t *testing.T) {
	e := fleet.CatalogEntry{
		Name:    "timesync.servers",
		Default: []any{"0.pool.ntp.org", "1.pool.ntp.org"},
	}

	slots, values := listSlots(e, "")
	if len(slots) < 4 {
		t.Errorf("got %d slots, want at least four so adding one needs no second save", len(slots))
	}
	if !strings.Contains(strings.Join(slots, "\n"), "0.pool.ntp.org") {
		t.Errorf("the declared defaults are not offered as placeholders: %v", slots)
	}
	for _, v := range values {
		if v != "" {
			t.Errorf("a default leaked into the VALUES (%q); saving would write it at this scope", v)
		}
	}

	// With a value set, that value is a value - and the slots still leave
	// room to add another.
	slots, values = listSlots(e, "time.example.org")
	if len(values) == 0 || values[0] != "time.example.org" {
		t.Errorf("the set value did not come back: %v", values)
	}
	if len(slots) < 4 {
		t.Errorf("got %d slots with a value set", len(slots))
	}
}

func TestRenderValueKeepsTypesDistinguishable(t *testing.T) {
	// A string renders bare so the page does not show quotes an operator did
	// not type; everything else goes through JSON so that true, "true" and 1
	// do not all read as the same thing.
	cases := []struct {
		in   any
		want string
	}{
		{"plasma", "plasma"},
		{true, "true"},
		{false, "false"},
		{42, "42"},
		{[]any{"a", "b"}, `["a","b"]`},
		{nil, "null"},
	}
	for _, c := range cases {
		if got := renderValue(c.in); got != c.want {
			t.Errorf("renderValue(%#v) = %q, want %q", c.in, got, c.want)
		}
	}
}
