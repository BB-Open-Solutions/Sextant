package main

import (
	"reflect"
	"testing"
)

// TestParseValue is table-driven over parseValue's full contract: CLI values
// that are valid, self-contained JSON stay typed (bool/number/string/null/
// array/object); everything else - including a JSON value followed by
// trailing garbage, or plain text that never parses - falls back to the
// original string verbatim. That fallback is what lets an operator type
// "settings set org theme dark" without quoting "dark" as JSON.
func TestParseValue(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want any
	}{
		{"bool true", "true", true},
		{"bool false", "false", false},
		{"positive integer", "42", float64(42)},
		{"negative integer", "-7", float64(-7)},
		{"float", "3.14", float64(3.14)},
		{"exponent notation", "1e3", float64(1000)},
		{"quoted JSON string", `"hello"`, "hello"},
		{"JSON null", "null", nil},
		{"bare word, not JSON", "hello", "hello"},
		{"empty string", "", ""},
		{"whitespace only", "   ", "   "},
		{"JSON array", "[1,2,3]", []any{float64(1), float64(2), float64(3)}},
		{"empty JSON array", "[]", []any{}},
		{"JSON object", `{"a":1,"b":"x"}`, map[string]any{"a": float64(1), "b": "x"}},
		{"empty JSON object", "{}", map[string]any{}},
		{"nested JSON object", `{"a":{"b":[1,2]}}`, map[string]any{
			"a": map[string]any{"b": []any{float64(1), float64(2)}},
		}},
		// Trailing content after a complete JSON value disqualifies the
		// whole thing - it is not "JSON with garbage", it's a string that
		// happens to start with a number or brace.
		{"number with trailing garbage", "42 extra", "42 extra"},
		{"object with trailing garbage", `{"a":1} junk`, `{"a":1} junk`},
		{"comma-separated, not an array", "1,2,3", "1,2,3"},
		{"malformed object", `{"a":}`, `{"a":}`},
		{"unquoted non-JSON word that looks numeric-ish", "NaN", "NaN"},
		{"leading/trailing whitespace around valid JSON", "  42  ", float64(42)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseValue(tc.in)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("parseValue(%q) = %#v, want %#v", tc.in, got, tc.want)
			}
		})
	}
}
