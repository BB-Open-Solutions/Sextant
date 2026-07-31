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

func TestDeviceConfigState(t *testing.T) {
	cases := []struct {
		name                           string
		revision, target               string
		online, hasStatus, coreChanged bool
		want                           string
	}{
		{"never seen", "", "", false, false, false, ""},
		{"status without a revision", "", "abc", false, true, false, ""},
		{"on its pin", "abc", "abc", true, true, false, configCurrent},
		{"on its pin, offline", "abc", "abc", false, true, false, configCurrent},
		{"follows HEAD, nothing says it is behind", "abc", "", true, true, false, configCurrent},
		// The distinction this vocabulary exists for. Same core means settings
		// are moving, not the system; calling that an update teaches an
		// operator to read every lag as a system change and then to ignore the
		// ones that are.
		{"behind on the core, checking in", "abc", "def", true, true, true, configUpdating},
		{"behind on the core, silent", "abc", "def", false, true, true, configPending},
		{"behind on settings only, checking in", "abc", "def", true, true, false, configApplying},
		{"behind on settings only, silent", "abc", "def", false, true, false, configSettingsDue},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := deviceConfigState(c.revision, c.target, c.online, c.hasStatus, c.coreChanged); got != c.want {
				t.Errorf("got %q, want %q", got, c.want)
			}
		})
	}

	// Both templates look the label up as devices.config_<state>, so a state
	// without a catalog entry renders its own key at the operator. Nothing
	// else ties the two together.
	for _, state := range []string{configCurrent, configUpdating, configPending} {
		for _, loc := range []string{"en", "nl"} {
			if catalog[loc]["devices.config_"+state] == "" {
				t.Errorf("devices.config_%s missing from the %s catalog", state, loc)
			}
		}
	}
}

// The two verdicts answer different questions, and the asymmetry between them
// is the point: a device can be up to date without being on spec (settings
// lag), but never on spec while out of date - if the revision matches, the
// core matches by construction. A display that let the second happen would be
// claiming something impossible.
func TestDeviceVerdictSeparatesSystemFromConfiguration(t *testing.T) {
	cases := []struct {
		name                           string
		revision, target               string
		online, hasStatus, coreChanged bool
		wantKnown, wantUp, wantSpec    bool
	}{
		{"never reported", "", "", false, false, false, false, false, false},
		{"on its pin", "abc", "abc", true, true, false, true, true, true},
		{"follows HEAD", "abc", "", true, true, false, true, true, true},
		{"settings lag only", "abc", "def", true, true, false, true, true, false},
		{"core lag", "abc", "def", true, true, true, true, false, false},
		{"core lag, silent", "abc", "def", false, true, true, true, false, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			v := judgeDevice(c.revision, c.target, c.online, c.hasStatus, c.coreChanged)
			if v.Known != c.wantKnown || v.UpToDate != c.wantUp || v.OnSpec != c.wantSpec {
				t.Errorf("got known=%v up=%v spec=%v, want %v/%v/%v",
					v.Known, v.UpToDate, v.OnSpec, c.wantKnown, c.wantUp, c.wantSpec)
			}
			// The impossible combination, asserted everywhere rather than
			// reasoned about once.
			if v.OnSpec && !v.UpToDate {
				t.Error("on spec while out of date: the revision matched but the core did not, which cannot happen")
			}
		})
	}
}
