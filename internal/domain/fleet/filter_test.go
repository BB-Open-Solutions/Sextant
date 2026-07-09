package fleet

import "testing"

func filterFleet(t *testing.T) *Fleet {
	t.Helper()
	const j = `{
	  "version": 3,
	  "groups": {"parent": {}, "child": {"parent": "parent"}},
	  "devices": {
	    "a": {"groups": ["child"], "hardware": "hp-g4", "class": "laptop",
	          "assignedUser": "ada", "labels": {"site": "hq", "ring": "canary"}},
	    "b": {"groups": ["parent"], "hardware": "t495s", "class": "server"}
	  }
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestFilterRules(t *testing.T) {
	f := filterFleet(t)
	cases := []struct {
		name string
		rule FilterRule
		a, b bool
	}{
		{"tag eq", FilterRule{Attr: AttrTag, Op: OpEq, Value: "a"}, true, false},
		{"tag ne", FilterRule{Attr: AttrTag, Op: OpNe, Value: "a"}, false, true},
		{"class eq", FilterRule{Attr: AttrClass, Op: OpEq, Value: "laptop"}, true, false},
		{"hardware prefix", FilterRule{Attr: AttrHardware, Op: OpPrefix, Value: "hp-"}, true, false},
		{"hardware in", FilterRule{Attr: AttrHardware, Op: OpIn, Values: []string{"t495s", "msi"}}, false, true},
		{"assignedUser eq", FilterRule{Attr: AttrAssignedUser, Op: OpEq, Value: "ada"}, true, false},
		{"label eq", FilterRule{Attr: "label:site", Op: OpEq, Value: "hq"}, true, false},
		{"label missing", FilterRule{Attr: "label:nope", Op: OpEq, Value: "x"}, false, false},
		// group membership includes ancestry: a is in child, child of parent.
		{"group direct", FilterRule{Attr: AttrGroup, Op: OpEq, Value: "child"}, true, false},
		{"group ancestor", FilterRule{Attr: AttrGroup, Op: OpEq, Value: "parent"}, true, true},
		{"group ne", FilterRule{Attr: AttrGroup, Op: OpNe, Value: "child"}, false, true},
		{"group prefix", FilterRule{Attr: AttrGroup, Op: OpPrefix, Value: "chi"}, true, false},
		{"group in", FilterRule{Attr: AttrGroup, Op: OpIn, Values: []string{"child", "x"}}, true, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fl := Filter{Rules: []FilterRule{tc.rule}}
			if got := f.matchesFilter(fl, "a"); got != tc.a {
				t.Errorf("device a: got %v, want %v", got, tc.a)
			}
			if got := f.matchesFilter(fl, "b"); got != tc.b {
				t.Errorf("device b: got %v, want %v", got, tc.b)
			}
		})
	}
}

func TestFilterMatchModes(t *testing.T) {
	f := filterFleet(t)
	laptop := FilterRule{Attr: AttrClass, Op: OpEq, Value: "laptop"}
	hq := FilterRule{Attr: "label:site", Op: OpEq, Value: "hq"}
	t495 := FilterRule{Attr: AttrHardware, Op: OpEq, Value: "t495s"}

	// all: both rules must hold.
	if !f.matchesFilter(Filter{Match: MatchAll, Rules: []FilterRule{laptop, hq}}, "a") {
		t.Error("all: a should match laptop+hq")
	}
	if f.matchesFilter(Filter{Match: MatchAll, Rules: []FilterRule{laptop, t495}}, "a") {
		t.Error("all: a is not a t495s")
	}
	// any: one suffices.
	if !f.matchesFilter(Filter{Match: MatchAny, Rules: []FilterRule{t495, hq}}, "a") {
		t.Error("any: a is at hq")
	}
	if f.matchesFilter(Filter{Match: MatchAny, Rules: []FilterRule{t495, hq}}, "b") == false {
		// b is a t495s.
		t.Error("any: b is a t495s")
	}
	// unknown device never matches.
	if f.matchesFilter(Filter{Rules: []FilterRule{laptop}}, "ghost") {
		t.Error("unknown device matched")
	}
	// empty rule set selects nothing (fail closed).
	if f.matchesFilter(Filter{}, "a") {
		t.Error("empty filter matched")
	}
}

func TestValidateFilter(t *testing.T) {
	ok := Filter{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq, Value: "laptop"}}}
	if err := ValidateFilter(ok); err != nil {
		t.Fatal(err)
	}
	bad := []Filter{
		{Match: "some", Rules: ok.Rules}, // bad match mode
		{},                               // no rules
		{Rules: []FilterRule{{Attr: "cpu", Op: OpEq, Value: "x"}}},        // unknown attr
		{Rules: []FilterRule{{Attr: "label:", Op: OpEq, Value: "x"}}},     // empty label key
		{Rules: []FilterRule{{Attr: AttrClass, Op: "regex", Value: "x"}}}, // unknown op
		{Rules: []FilterRule{{Attr: AttrClass, Op: OpEq}}},                // eq without value
		{Rules: []FilterRule{{Attr: AttrClass, Op: OpIn}}},                // in without values
	}
	for i, fl := range bad {
		if err := ValidateFilter(fl); err == nil {
			t.Errorf("case %d: invalid filter accepted", i)
		}
	}
}

func TestValidators(t *testing.T) {
	for _, ok := range []string{"firefox", "python3Packages.requests", "org.mozilla.firefox"} {
		if !ValidatePackage(ok) {
			t.Errorf("package %q rejected", ok)
		}
	}
	for _, bad := range []string{"", "rm -rf", "a b", `x"y`, "${pwn}", "../up", "a..b", "/etc"} {
		if ValidatePackage(bad) {
			t.Errorf("package %q accepted", bad)
		}
	}
	if !ValidateOverlay("my-overlay_2") || ValidateOverlay("a.b") || ValidateOverlay("") {
		t.Error("overlay validator wrong")
	}
	for _, ok := range []string{"pilot", "lt-1", "a"} {
		if !ValidateSlug(ok) {
			t.Errorf("slug %q rejected", ok)
		}
	}
	for _, bad := range []string{"", "Pilot", "-x", "a_b", "a b", "x/../y", strings64()} {
		if ValidateSlug(bad) {
			t.Errorf("slug %q accepted", bad)
		}
	}
}

func strings64() string {
	b := make([]byte, 64)
	for i := range b {
		b[i] = 'a'
	}
	return string(b)
}

func TestResolveApps(t *testing.T) {
	const j = `{
	  "version": 3,
	  "org": {"packages": ["firefox"], "flatpaks": ["org.gimp.GIMP"]},
	  "groups": {
	    "parent": {"packages": ["vlc"]},
	    "child":  {"parent": "parent", "packages": ["vlc", "inkscape"], "overlays": ["corp-branding"]}
	  },
	  "devices": {"d": {"groups": ["child"], "hardware": "hw", "packages": ["htop"]}}
	}`
	f, err := Decode([]byte(j))
	if err != nil {
		t.Fatal(err)
	}
	pkgs, flats, ovs := f.ResolveApps("d")
	wantList(t, "packages", pkgs, []string{"firefox", "htop", "inkscape", "vlc"})
	wantList(t, "flatpaks", flats, []string{"org.gimp.GIMP"})
	wantList(t, "overlays", ovs, []string{"corp-branding"})
}

func wantList(t *testing.T, name string, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s = %v, want %v", name, got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%s = %v, want %v", name, got, want)
		}
	}
}
