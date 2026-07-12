package fleet

import "testing"

const sampleProfiles = `[
  {"name":"lenovo-t495s","vendor":"Lenovo","models":["ThinkPad T495s","20QH"],
   "disko":"LUKS on nvme0n1",
   "steps":[{"title":"Enter firmware","detail":"Tap the key at the logo","key":"Enter"},
            {"title":"Disable Secure Boot","detail":"Security > Secure Boot > Disabled"}]},
  {"name":"hp-probook-440","vendor":"HP","models":["ProBook 440"],
   "steps":[{"title":"Enter firmware","key":"F10"}]}
]`

func TestParseHardwareProfiles(t *testing.T) {
	hp, err := ParseHardwareProfiles([]byte(sampleProfiles))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if hp.Len() != 2 {
		t.Fatalf("len = %d, want 2", hp.Len())
	}
	// Stable, sorted name order for rendering.
	names := hp.Names()
	if names[0] != "hp-probook-440" || names[1] != "lenovo-t495s" {
		t.Fatalf("names not sorted: %v", names)
	}
	p, ok := hp.Get("lenovo-t495s")
	if !ok || p.Vendor != "Lenovo" || len(p.Steps) != 2 || p.Steps[0].Key != "Enter" {
		t.Fatalf("profile not parsed: %+v", p)
	}
	if !hp.Has("hp-probook-440") || hp.Has("nope") {
		t.Fatal("Has wrong")
	}
}

func TestParseHardwareProfilesEmptyAndBad(t *testing.T) {
	hp, err := ParseHardwareProfiles(nil)
	if err != nil || hp == nil || hp.Len() != 0 {
		t.Fatalf("empty input should yield empty set: %v %v", hp, err)
	}
	if _, err := ParseHardwareProfiles([]byte(`[{"vendor":"x"}]`)); err == nil {
		t.Fatal("profile without name accepted")
	}
	if _, err := ParseHardwareProfiles([]byte(`[{"name":"a"},{"name":"a"}]`)); err == nil {
		t.Fatal("duplicate profile accepted")
	}
	if _, err := ParseHardwareProfiles([]byte(`not json`)); err == nil {
		t.Fatal("malformed json accepted")
	}
}

func TestHardwareProfilesSuggest(t *testing.T) {
	hp, _ := ParseHardwareProfiles([]byte(sampleProfiles))
	// Discovered model contains a mapped substring -> profile suggested.
	if got := hp.Suggest("LENOVO", "ThinkPad T495s Gen 1"); got != "lenovo-t495s" {
		t.Fatalf("suggest lenovo = %q", got)
	}
	if got := hp.Suggest("HP", "HP ProBook 440 G8"); got != "hp-probook-440" {
		t.Fatalf("suggest hp = %q", got)
	}
	// No match -> empty, operator picks manually.
	if got := hp.Suggest("Dell", "Latitude 5420"); got != "" {
		t.Fatalf("suggest unknown = %q, want empty", got)
	}
	if got := hp.Suggest("", ""); got != "" {
		t.Fatalf("suggest empty = %q", got)
	}
}

func TestHardwareSpecEmpty(t *testing.T) {
	if !(HardwareSpec{}).Empty() {
		t.Fatal("zero spec should be empty")
	}
	if (HardwareSpec{Model: "x"}).Empty() {
		t.Fatal("populated spec should not be empty")
	}
}
