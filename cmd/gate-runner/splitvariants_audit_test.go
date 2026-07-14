package main

import (
	"reflect"
	"testing"
)

// TestSplitVariants covers the untested edges the doc comment calls out:
// an empty string means no variants, but an empty ENTRY inside a
// comma-separated list is meaningful (the bare tag itself alongside a
// suffixed variant) and must be preserved, not dropped.
func TestSplitVariants(t *testing.T) {
	cases := []struct {
		in   string
		want []string
	}{
		{"", nil},
		{"-sb", []string{"-sb"}},
		{",-sb", []string{"", "-sb"}},
		{"-sb,-tpm", []string{"-sb", "-tpm"}},
	}
	for _, c := range cases {
		got := splitVariants(c.in)
		if !reflect.DeepEqual(got, c.want) {
			t.Errorf("splitVariants(%q) = %#v, want %#v", c.in, got, c.want)
		}
	}
}
