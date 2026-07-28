package web

import (
	"slices"
	"testing"
)

func sampleRows() []deviceRow {
	return []deviceRow{
		{Tag: "lt-ada", Class: "laptop", Hardware: "t495s", AssignedUser: "ada", Groups: []string{"pilot"}, HasStatus: true, Online: true, Baseline: "ok"},
		{Tag: "srv-1", Class: "server", Hardware: "msi", Groups: []string{"zaanstad"}, HasStatus: true, Online: false, Baseline: "attention"},
		{Tag: "lt-new", Class: "laptop", Hardware: "hp-g4", Groups: []string{"pilot"}, Baseline: "attention"},
	}
}

func rowTags(rs []deviceRow) []string {
	out := make([]string, len(rs))
	for i, r := range rs {
		out[i] = r.Tag
	}
	return out
}

func TestFilterDeviceRows(t *testing.T) {
	cases := []struct {
		name                              string
		q, class, group, status, baseline string
		want                              []string
	}{
		{"search by user", "ada", "", "", "", "", []string{"lt-ada"}},
		{"search by hardware", "msi", "", "", "", "", []string{"srv-1"}},
		{"search by tag prefix", "lt-", "", "", "", "", []string{"lt-ada", "lt-new"}},
		{"class facet", "", "server", "", "", "", []string{"srv-1"}},
		{"group facet", "", "", "pilot", "", "", []string{"lt-ada", "lt-new"}},
		{"status online", "", "", "", "online", "", []string{"lt-ada"}},
		{"status offline", "", "", "", "offline", "", []string{"srv-1"}},
		{"status never", "", "", "", "never", "", []string{"lt-new"}},
		{"baseline ok", "", "", "", "", "ok", []string{"lt-ada"}},
		{"baseline attention", "", "", "", "", "attention", []string{"srv-1", "lt-new"}},
		{"combined", "lt", "laptop", "pilot", "online", "ok", []string{"lt-ada"}},
		{"no match", "nope", "", "", "", "", []string{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := rowTags(filterDeviceRows(sampleRows(), c.q, c.class, c.group, c.status, c.baseline))
			if !slices.Equal(got, c.want) {
				t.Errorf("got %v, want %v", got, c.want)
			}
		})
	}
}

func TestSortDeviceRows(t *testing.T) {
	mk := func() []deviceRow {
		return []deviceRow{
			{Tag: "b", Hardware: "z", HasStatus: true, Online: false, Baseline: "ok"},
			{Tag: "a", Hardware: "y", HasStatus: true, Online: true, Baseline: "attention"},
			{Tag: "c", Hardware: "x", HasStatus: false}, // retired: not judged
		}
	}
	cases := []struct {
		key, dir string
		want     []string
	}{
		{"tag", "asc", []string{"a", "b", "c"}},
		{"tag", "desc", []string{"c", "b", "a"}},
		{"", "asc", []string{"a", "b", "c"}},          // default = tag
		{"status", "asc", []string{"a", "b", "c"}},    // online, offline, never
		{"status", "desc", []string{"c", "b", "a"}},   // never, offline, online
		{"hardware", "asc", []string{"c", "a", "b"}},  // x(c), y(a), z(b)
		{"baseline", "asc", []string{"a", "b", "c"}},  // attention, ok, not judged
		{"baseline", "desc", []string{"c", "b", "a"}}, // reversed
	}
	for _, c := range cases {
		rows := mk()
		sortDeviceRows(rows, c.key, c.dir)
		if got := rowTags(rows); !slices.Equal(got, c.want) {
			t.Errorf("sort %q/%q = %v, want %v", c.key, c.dir, got, c.want)
		}
	}
}
