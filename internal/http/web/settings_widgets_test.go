package web

import (
	"strings"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
)

// TestGateSortsFirst: a card that led with options reading "takes effect once
// printing.enable is on" and put printing.enable last asked the operator to
// read bottom-up. Reported from the production console 2026-08-06 for
// printing, diskUnlock, elevationRequests and cacheAuth - one ordering rule
// covers all four, because the gate relationship is already in the catalog.
func TestGateSortsFirst(t *testing.T) {
	cat := mustCatalog(t, `[
	  {"name":"printing.discover","type":"boolean","description":"d"},
	  {"name":"printing.drivers","type":"one of \"open\", \"broad\"","description":"d"},
	  {"name":"printing.enable","type":"boolean","description":"d"}
	]`)
	got := cat.ByCategory("printing")
	if len(got) != 3 || got[0].Name != "printing.enable" {
		t.Fatalf("gate is not first: %v", names(got))
	}
	// The rest keeps a stable, predictable order rather than whatever the
	// file happened to hold.
	if got[1].Name != "printing.discover" || got[2].Name != "printing.drivers" {
		t.Fatalf("order after the gate = %v", names(got))
	}
}

// TestNestedGateSortsFirst: diskUnlock.tpm2.device is gated by
// diskUnlock.tpm2.enable, not by a broader diskUnlock.enable that does not
// exist. Requires walks outward, so the nested gate wins.
func TestNestedGateSortsFirst(t *testing.T) {
	cat := mustCatalog(t, `[
	  {"name":"diskUnlock.tpm2.device","type":"string","description":"d"},
	  {"name":"diskUnlock.tpm2.enable","type":"boolean","description":"d"}
	]`)
	got := cat.ByCategory("diskUnlock")
	if got[0].Name != "diskUnlock.tpm2.enable" {
		t.Fatalf("nested gate not first: %v", names(got))
	}
}

// TestWidgetHintOverridesTypeButNeverSecret: the hint exists because a type
// cannot say "this string is a time range". It must not be able to turn a
// secret-ref picker into a text box - that one is a safety property, not a
// presentation choice.
func TestWidgetHintOverridesTypeButNeverSecret(t *testing.T) {
	plain := fleet.CatalogEntry{Name: "updates.maintenanceWindow", Type: "string"}
	if plain.Widget() != fleet.WidgetText {
		t.Fatalf("unhinted string = %s", plain.Widget())
	}
	hinted := plain
	hinted.WidgetHint = "timerange"
	if hinted.Widget() != fleet.WidgetTimeRange {
		t.Fatalf("hinted = %s", hinted.Widget())
	}
	// An annotation nobody implemented falls back to the type instead of
	// rendering nothing.
	bogus := plain
	bogus.WidgetHint = "hologram"
	if bogus.Widget() != fleet.WidgetText {
		t.Fatalf("unknown hint = %s, want the type-derived widget", bogus.Widget())
	}
	sec := fleet.CatalogEntry{Name: "netbird.setupKey", Type: "string", Secret: true, WidgetHint: "text"}
	if sec.Widget() != fleet.WidgetSecret {
		t.Fatal("a hint overrode the secret-ref picker; the material could be typed in")
	}
}

// TestTimeRangeIsValidatedNotJustRendered: the console is not the only writer -
// the API and sxctl reach the same setting - so a check that lives in a
// template constrains nothing.
func TestTimeRangeIsValidatedNotJustRendered(t *testing.T) {
	e := fleet.CatalogEntry{Name: "updates.maintenanceWindow", Type: "string", WidgetHint: "timerange"}
	for _, ok := range []string{"22:00-06:00", "00:00-23:59", "09:30-17:00"} {
		if _, err := e.ParseValue(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"9:00-17:00", "22:00", "25:00-06:00", "22:00-06:60", "evenings"} {
		if _, err := e.ParseValue(bad); err == nil {
			t.Errorf("%q accepted as a time range", bad)
		}
	}
}

// TestCombineSubmittedJoinsMultiFieldWidgets: both new widgets post several
// fields under one name, and the rule that reassembles them lives in one
// place because the parser is what decides what a value looks like.
func TestCombineSubmittedJoinsMultiFieldWidgets(t *testing.T) {
	tr := fleet.CatalogEntry{Name: "w", Type: "string", WidgetHint: "timerange"}
	if got := combineSubmitted(tr, []string{"22:00", "06:00"}); got != "22:00-06:00" {
		t.Fatalf("range = %q", got)
	}
	// Half a window is not a window: it would reach the device unparseable,
	// so it reads as "cleared" instead.
	if got := combineSubmitted(tr, []string{"22:00", ""}); got != "" {
		t.Fatalf("half a range = %q, want empty", got)
	}

	fl := fleet.CatalogEntry{Name: "s", Type: "list of string", WidgetHint: "fixedlist"}
	got := combineSubmitted(fl, []string{"a", "", "b", ""})
	if got != "a\nb" {
		t.Fatalf("fixed list = %q, want blanks dropped", got)
	}
	if _, err := fl.ParseValue(got); err != nil {
		t.Fatalf("what the form assembles must parse: %v", err)
	}
}

// TestListSlotsKeepDefaultsAsPlaceholders: prefilling the declared defaults as
// VALUES would write them explicitly the moment the page is saved for any
// reason, moving the row from "inherits" to "modified here". This page exists
// to make inheritance visible, so the defaults stay placeholders.
func TestListSlotsKeepDefaultsAsPlaceholders(t *testing.T) {
	e := fleet.CatalogEntry{
		Name: "timesync.options.servers", Type: "list of string", WidgetHint: "fixedlist",
		Default: []any{"ntp.time.nl", "0.nl.pool.ntp.org", "1.nl.pool.ntp.org"},
	}
	slots, values := listSlots(e, "")
	if len(slots) != 4 {
		t.Fatalf("slots = %d, want the three defaults plus a spare", len(slots))
	}
	if slots[0] != "ntp.time.nl" || slots[3] != "" {
		t.Fatalf("slots = %v", slots)
	}
	for i, v := range values {
		if v != "" {
			t.Fatalf("slot %d prefilled with %q; an unset scope must stay unset", i, v)
		}
	}
	// A scope that HAS a value shows it, index-aligned with the slots.
	_, values = listSlots(e, "a\nb")
	if values[0] != "a" || values[1] != "b" || values[2] != "" {
		t.Fatalf("set values = %v", values)
	}
}

// mustCatalog parses a catalog the way production does. A hand-built
// fleet.Catalog has no name index, so Lookup - and therefore the gate
// relationship - silently answers "no" for everything.
func mustCatalog(t *testing.T, raw string) *fleet.Catalog {
	t.Helper()
	c, err := fleet.ParseCatalog([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func names(entries []fleet.CatalogEntry) []string {
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		out = append(out, e.Name)
	}
	return out
}

// TestFooterStatusLinkFollowsTheListener: with a separate metrics listener
// /status is not on this port, so the footer link 404s for everyone - being
// logged in does not help, the split is by port. Hide the door rather than
// offer one that cannot open.
func TestFooterStatusLinkFollowsTheListener(t *testing.T) {
	var s Server
	s.SetStatusOnMain(false)
	if s.statusOnMain {
		t.Fatal("status link left on with a separate metrics listener")
	}
	s.SetStatusOnMain(true)
	if !s.statusOnMain {
		t.Fatal("status link hidden when /status is served here")
	}
	// And the template only renders it behind that flag.
	src, err := assets.ReadFile("templates/layout.html")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(src), `{{if .HasStatusPage}}<a href="/status"`) {
		t.Fatal("the footer links /status unconditionally again")
	}
}
