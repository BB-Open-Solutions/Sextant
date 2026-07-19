package fleet

import (
	"strings"
	"testing"
)

func TestParseProfilesEmpty(t *testing.T) {
	ps, err := ParseProfiles(nil)
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if ps.Len() != 0 {
		t.Fatalf("expected empty set, got %d", ps.Len())
	}
}

func TestParseProfiles(t *testing.T) {
	raw := []byte(`[
	  {"name":"laptop","label":"Laptop workplace","class":"laptop",
	   "settings":{"desktop.environment":"gnome","ntp.enable":true}},
	  {"name":"infra","settings":{"ntp.enable":true}}
	]`)
	ps, err := ParseProfiles(raw)
	if err != nil {
		t.Fatal(err)
	}
	if ps.Len() != 2 {
		t.Fatalf("want 2 profiles, got %d", ps.Len())
	}
	// All is name-sorted regardless of document order.
	if all := ps.All(); all[0].Name != "infra" || all[1].Name != "laptop" {
		t.Fatalf("unexpected order: %q, %q", all[0].Name, all[1].Name)
	}
	p, ok := ps.Get("laptop")
	if !ok || p.Class != "laptop" || p.Label != "Laptop workplace" {
		t.Fatalf("laptop profile mangled: %+v", p)
	}
}

func TestParseProfilesRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"bad slug":    `[{"name":"Not A Slug","settings":{"a":1}}]`,
		"no settings": `[{"name":"laptop"}]`,
		"duplicate":   `[{"name":"laptop","settings":{"a":1}},{"name":"laptop","settings":{"a":2}}]`,
		"malformed":   `{"name":`,
	} {
		if _, err := ParseProfiles([]byte(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestProfileHashTracksContent(t *testing.T) {
	a := Profile{Name: "laptop", Class: "laptop", Settings: map[string]any{"x": 1}}
	b := a
	if a.Hash() != b.Hash() {
		t.Fatal("identical profiles must hash identically")
	}
	// Wording changes do not move the hash; content changes do.
	b.Description = "reworded"
	if a.Hash() != b.Hash() {
		t.Fatal("description must not affect the hash")
	}
	b.Settings = map[string]any{"x": 2}
	if a.Hash() == b.Hash() {
		t.Fatal("settings change must move the hash")
	}
	if !strings.HasPrefix(a.Provenance(), "laptop@") {
		t.Fatalf("provenance format: %q", a.Provenance())
	}
}

func testFleet() *Fleet {
	return &Fleet{
		Groups:  map[string]Group{"zaanstad": {}},
		Devices: map[string]Device{"t495s": {Class: "laptop", Groups: []string{"zaanstad"}}},
	}
}

func TestApplyProfile(t *testing.T) {
	f := testFleet()
	p := Profile{Name: "laptop", Label: "Laptop", Class: "laptop",
		Settings: map[string]any{"ntp.enable": true}}
	if err := ApplyProfile(p)(f); err != nil {
		t.Fatal(err)
	}
	pol, ok := f.Policies["laptop"]
	if !ok {
		t.Fatal("policy not created")
	}
	if pol.Profile != p.Provenance() {
		t.Fatalf("provenance %q, want %q", pol.Profile, p.Provenance())
	}
	fl, ok := f.Filters["class-laptop"]
	if !ok || len(fl.Rules) != 1 || fl.Rules[0].Attr != AttrClass || fl.Rules[0].Value != "laptop" {
		t.Fatalf("class filter wrong: %+v", fl)
	}
	if len(f.Assignments) != 1 || f.Assignments[0].Target != "org" || f.Assignments[0].Filter != "class-laptop" {
		t.Fatalf("assignment wrong: %+v", f.Assignments)
	}
}

func TestApplyProfileReapplyRefreshes(t *testing.T) {
	f := testFleet()
	p := Profile{Name: "laptop", Class: "laptop", Settings: map[string]any{"ntp.enable": true}}
	if err := ApplyProfile(p)(f); err != nil {
		t.Fatal(err)
	}
	// The profile moves on; re-apply refreshes settings and provenance
	// without duplicating filter or assignment.
	p.Settings = map[string]any{"ntp.enable": false}
	if err := ApplyProfile(p)(f); err != nil {
		t.Fatal(err)
	}
	if v := f.Policies["laptop"].Settings["ntp.enable"]; v != false {
		t.Fatalf("settings not refreshed: %v", v)
	}
	if f.Policies["laptop"].Profile != p.Provenance() {
		t.Fatal("provenance not refreshed")
	}
	if len(f.Assignments) != 1 {
		t.Fatalf("assignment duplicated: %+v", f.Assignments)
	}
}

func TestApplyProfileRefusesHandMadeCollision(t *testing.T) {
	f := testFleet()
	f.Policies = map[string]Policy{"laptop": {Settings: map[string]any{"a": 1}}}
	p := Profile{Name: "laptop", Settings: map[string]any{"b": 2}}
	if err := ApplyProfile(p)(f); err == nil {
		t.Fatal("expected refusal over hand-made policy")
	}
	if f.Policies["laptop"].Settings["a"] != float64(1) && f.Policies["laptop"].Settings["a"] != 1 {
		t.Fatalf("hand-made policy clobbered: %+v", f.Policies["laptop"])
	}
}

func TestApplyProfileKeepsHandTunedFilter(t *testing.T) {
	f := testFleet()
	// An operator already narrowed class-laptop further; applying the
	// profile must reuse, never overwrite, that filter.
	f.Filters = map[string]Filter{"class-laptop": {Rules: []FilterRule{
		{Attr: AttrClass, Op: OpEq, Value: "laptop"},
		{Attr: AttrGroup, Op: OpEq, Value: "zaanstad"},
	}}}
	p := Profile{Name: "laptop", Class: "laptop", Settings: map[string]any{"a": 1}}
	if err := ApplyProfile(p)(f); err != nil {
		t.Fatal(err)
	}
	if len(f.Filters["class-laptop"].Rules) != 2 {
		t.Fatalf("hand-tuned filter overwritten: %+v", f.Filters["class-laptop"])
	}
}

func TestApplyProfileClasslessSkipsFilter(t *testing.T) {
	f := testFleet()
	p := Profile{Name: "base", Settings: map[string]any{"a": 1}}
	if err := ApplyProfile(p)(f); err != nil {
		t.Fatal(err)
	}
	if len(f.Filters) != 0 {
		t.Fatalf("unexpected filter: %+v", f.Filters)
	}
	if f.Assignments[0].Filter != "" {
		t.Fatalf("assignment should be unfiltered: %+v", f.Assignments[0])
	}
}
