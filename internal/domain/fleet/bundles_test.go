package fleet

import "testing"

func TestParseBundlesEmpty(t *testing.T) {
	bs, err := ParseBundles(nil)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Len() != 0 {
		t.Fatalf("want empty, got %d", bs.Len())
	}
}

func TestParseBundles(t *testing.T) {
	raw := []byte(`[
	  {"name":"identity","label":"Identity & login","icon":"badge",
	   "settings":{"identity.enable":true,"hardening.screenLock":true,
	     "identity.offlineLoginDays":30},
	   "exposed":["identity.offlineLoginDays"]},
	  {"name":"vpn","settings":{"netbird.enable":true}}
	]`)
	bs, err := ParseBundles(raw)
	if err != nil {
		t.Fatal(err)
	}
	if bs.Len() != 2 {
		t.Fatalf("want 2, got %d", bs.Len())
	}
	// All is name-sorted.
	if all := bs.All(); all[0].Name != "identity" || all[1].Name != "vpn" {
		t.Fatalf("order: %q %q", all[0].Name, all[1].Name)
	}
	id, _ := bs.Get("identity")
	if id.EnableKey() != "identity.enable" {
		t.Fatalf("enable key = %q", id.EnableKey())
	}
	if !id.IsExposed("identity.offlineLoginDays") || id.IsExposed("hardening.screenLock") {
		t.Fatal("exposed set wrong")
	}
}

func TestParseBundlesRejects(t *testing.T) {
	for name, raw := range map[string]string{
		"bad slug":       `[{"name":"Not Slug","settings":{"a":1}}]`,
		"no settings":    `[{"name":"x"}]`,
		"exposed unset":  `[{"name":"x","settings":{"a":1},"exposed":["b"]}]`,
		"duplicate":      `[{"name":"x","settings":{"a":1}},{"name":"x","settings":{"a":2}}]`,
		"malformed json": `{`,
	} {
		if _, err := ParseBundles([]byte(raw)); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}

func TestBundleEnableKeyFallback(t *testing.T) {
	// No explicit Enable: first .enable key (sorted) wins.
	b := Bundle{Name: "x", Settings: map[string]any{
		"z.other": 1, "a.enable": true, "b.enable": true}}
	if b.EnableKey() != "a.enable" {
		t.Fatalf("enable fallback = %q", b.EnableKey())
	}
	// Explicit Enable wins.
	b.Enable = "b.enable"
	if b.EnableKey() != "b.enable" {
		t.Fatalf("explicit enable = %q", b.EnableKey())
	}
	// No .enable anywhere.
	none := Bundle{Name: "y", Settings: map[string]any{"a.x": 1}}
	if none.EnableKey() != "" {
		t.Fatalf("no-enable = %q", none.EnableKey())
	}
}
