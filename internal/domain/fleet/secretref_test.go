package fleet

import "testing"

func TestSecretRefRegistry(t *testing.T) {
	f := &Fleet{Version: Version}
	if err := AddSecretRef("netbird-setupkey", SecretRef{Description: "NetBird join key"})(f); err != nil {
		t.Fatal(err)
	}
	if !f.HasSecretRef("netbird-setupkey") {
		t.Fatal("registered secret ref not found")
	}
	if f.HasSecretRef("nope") {
		t.Fatal("unknown ref reported present")
	}
	// Duplicate and non-slug names rejected.
	if err := AddSecretRef("netbird-setupkey", SecretRef{})(f); err == nil {
		t.Fatal("duplicate secret ref accepted")
	}
	if err := AddSecretRef("Not A Name", SecretRef{})(f); err == nil {
		t.Fatal("non-slug secret name accepted")
	}
	// Remove.
	if err := RemoveSecretRef("netbird-setupkey")(f); err != nil {
		t.Fatal(err)
	}
	if f.HasSecretRef("netbird-setupkey") {
		t.Fatal("removed secret ref still present")
	}
	if err := RemoveSecretRef("netbird-setupkey")(f); err == nil {
		t.Fatal("removing unknown ref accepted")
	}
}

func TestCatalogSecretWidgetRejectsRawValue(t *testing.T) {
	e := CatalogEntry{Name: "netbird.setupKeyFile", Type: "string", Secret: true}
	if e.Widget() != WidgetSecret {
		t.Fatalf("secret entry widget = %q, want secret", e.Widget())
	}
	// A reference name (slug) is accepted; a raw secret value is not.
	if _, err := e.ParseValue("netbird-setupkey"); err != nil {
		t.Fatalf("valid reference rejected: %v", err)
	}
	if _, err := e.ParseValue("nb_live_9f83aa22_the_actual_key=="); err == nil {
		t.Fatal("a raw secret value was accepted for a secret setting")
	}
	// Empty clears the reference (inherit / unset).
	if _, err := e.ParseValue(""); err != nil {
		t.Fatalf("empty reference rejected: %v", err)
	}
}
