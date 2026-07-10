package fleet

import "testing"

const sampleCatalog = `[
  {"name":"apps.office","type":"boolean","description":"LibreOffice suite","default":false},
  {"name":"apps.browser","type":"one of \"firefox\", \"chromium\"","description":"Default browser","default":"firefox"},
  {"name":"ssh.enable","type":"boolean","description":"OpenSSH daemon","riskClass":"high"},
  {"name":"autoUpgrade.rebootWindow","type":"positive integer","description":"Reboot window minutes","default":30},
  {"name":"hostnamePrefix","type":"string","description":"Device hostname prefix"}
]`

func TestParseCatalog(t *testing.T) {
	c, err := ParseCatalog([]byte(sampleCatalog))
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Entries) != 5 {
		t.Fatalf("entries = %d", len(c.Entries))
	}
	// Sorted by name.
	if c.Entries[0].Name != "apps.browser" {
		t.Fatalf("first entry = %s", c.Entries[0].Name)
	}
	if _, ok := c.Lookup("ssh.enable"); !ok {
		t.Fatal("lookup miss")
	}
	if _, ok := c.Lookup("nope"); ok {
		t.Fatal("phantom entry")
	}
	// Empty and nil catalogs are valid.
	for _, b := range [][]byte{nil, []byte("[]")} {
		c, err := ParseCatalog(b)
		if err != nil || len(c.Entries) != 0 {
			t.Fatalf("empty catalog = %+v, %v", c, err)
		}
	}
	if _, err := ParseCatalog([]byte("{broken")); err == nil {
		t.Fatal("malformed accepted")
	}
	// Duplicate names would let the rendered row and the POST-time lookup
	// disagree; the whole catalog is refused.
	dup := `[{"name":"x","type":"boolean","description":"a"},
	         {"name":"x","type":"string","description":"b"}]`
	if _, err := ParseCatalog([]byte(dup)); err == nil {
		t.Fatal("duplicate names accepted")
	}
}

func TestCatalogDefaultsAndRisk(t *testing.T) {
	c, _ := ParseCatalog([]byte(sampleCatalog))
	ssh, _ := c.Lookup("ssh.enable")
	if ssh.RiskClass != "high" {
		t.Fatalf("riskClass = %q", ssh.RiskClass)
	}
	if ssh.DefaultString() != "" {
		t.Fatalf("no-default renders %q", ssh.DefaultString())
	}
	cases := map[string]string{
		"apps.office":              "false",
		"apps.browser":             `"firefox"`,
		"autoUpgrade.rebootWindow": "30",
	}
	for name, want := range cases {
		e, _ := c.Lookup(name)
		if got := e.DefaultString(); got != want {
			t.Errorf("default(%s) = %q, want %q", name, got, want)
		}
		if e.RiskClass != "" {
			t.Errorf("%s grew riskClass %q", name, e.RiskClass)
		}
	}
}

func TestCatalogWidgets(t *testing.T) {
	cases := []struct {
		typ  string
		want Widget
	}{
		{"boolean", WidgetToggle},
		{"positive integer", WidgetNumber},
		{"signed integer", WidgetNumber},
		{`one of "a", "b"`, WidgetSelect},
		{"string", WidgetText},
		{"list of string", WidgetText},
	}
	for _, tc := range cases {
		if got := (CatalogEntry{Type: tc.typ}).Widget(); got != tc.want {
			t.Errorf("widget(%q) = %s, want %s", tc.typ, got, tc.want)
		}
	}
	opts := (CatalogEntry{Type: `one of "firefox", "chromium"`}).Options()
	if len(opts) != 2 || opts[0] != "firefox" || opts[1] != "chromium" {
		t.Fatalf("options = %v", opts)
	}
	if (CatalogEntry{Type: "boolean"}).Options() != nil {
		t.Fatal("boolean grew options")
	}
	// Widget and Options must agree on prefix detection: a type Widget
	// classifies as select must never yield zero options.
	mixed := CatalogEntry{Type: `One of "a", "b"`}
	if mixed.Widget() == WidgetSelect && len(mixed.Options()) == 0 {
		t.Fatal("select widget with zero options")
	}
}

func TestCatalogCategories(t *testing.T) {
	c, _ := ParseCatalog([]byte(sampleCatalog))
	cats := c.Categories()
	want := []string{"apps", "autoUpgrade", "general", "ssh"}
	if len(cats) != len(want) {
		t.Fatalf("categories = %v", cats)
	}
	for i := range want {
		if cats[i] != want[i] {
			t.Fatalf("categories = %v, want %v", cats, want)
		}
	}
	if got := c.ByCategory("apps"); len(got) != 2 {
		t.Fatalf("apps entries = %+v", got)
	}
	// Top-level option lands in "general".
	if (CatalogEntry{Name: "hostnamePrefix"}).Category() != "general" {
		t.Fatal("top-level category")
	}
}

func TestCatalogParseValue(t *testing.T) {
	b := CatalogEntry{Name: "b", Type: "boolean"}
	for in, want := range map[string]any{"true": true, "on": true, "false": false, "off": false, "": false} {
		got, err := b.ParseValue(in)
		if err != nil || got != want {
			t.Errorf("bool(%q) = %v, %v", in, got, err)
		}
	}
	if _, err := b.ParseValue("yes"); err == nil {
		t.Error("bool accepted yes")
	}

	n := CatalogEntry{Name: "n", Type: "positive integer, meaning >0"}
	if got, err := n.ParseValue("42"); err != nil || got != 42 {
		t.Errorf("int = %v, %v", got, err)
	}
	for _, bad := range []string{"", "4.2", "x", "42x", "0", "-3"} {
		if _, err := n.ParseValue(bad); err == nil {
			t.Errorf("positive int accepted %q", bad)
		}
	}
	u := CatalogEntry{Name: "u", Type: "unsigned integer"}
	if _, err := u.ParseValue("-1"); err == nil {
		t.Error("unsigned accepted -1")
	}
	if got, err := u.ParseValue("0"); err != nil || got != 0 {
		t.Errorf("unsigned 0 = %v, %v", got, err)
	}
	plain := CatalogEntry{Name: "p", Type: "signed integer"}
	if got, err := plain.ParseValue("-5"); err != nil || got != -5 {
		t.Errorf("signed -5 = %v, %v", got, err)
	}

	sel := CatalogEntry{Name: "s", Type: `one of "a", "b"`}
	if got, err := sel.ParseValue("a"); err != nil || got != "a" {
		t.Errorf("select = %v, %v", got, err)
	}
	if _, err := sel.ParseValue("c"); err == nil {
		t.Error("select accepted out-of-set value")
	}

	txt := CatalogEntry{Name: "t", Type: "string"}
	if got, _ := txt.ParseValue("hello"); got != "hello" {
		t.Errorf("text = %v", got)
	}
}
