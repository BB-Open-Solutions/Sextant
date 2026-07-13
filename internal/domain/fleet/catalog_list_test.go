package fleet

import (
	"reflect"
	"testing"
)

// TestParseValueList: a list-of type parses one item per line into a real
// []string (blank lines dropped), so the generator emits a nix list instead
// of a single string the gate would reject.
func TestParseValueList(t *testing.T) {
	e := CatalogEntry{Name: "timesync.options.servers", Type: "list of string"}
	if e.Widget() != WidgetCode {
		t.Fatalf("widget = %s, want code", e.Widget())
	}
	got, err := e.ParseValue("ntp.time.nl\n\n  0.nl.pool.ntp.org  \n")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"ntp.time.nl", "0.nl.pool.ntp.org"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseValue = %#v, want %#v", got, want)
	}
	if _, err := e.ParseValue("\n  \n"); err == nil {
		t.Fatal("an all-blank list must error, not store an empty list")
	}
}
