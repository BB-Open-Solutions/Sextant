package secret

import "testing"

func TestKindValidate(t *testing.T) {
	for _, k := range []Kind{LUKS, Admin} {
		if !k.Valid() {
			t.Errorf("%s should be valid", k)
		}
		if err := k.Validate(); err != nil {
			t.Errorf("%s validate: %v", k, err)
		}
	}
	for _, k := range []Kind{"", "tpm", "LUKS", "password"} {
		if k.Valid() {
			t.Errorf("%q should be invalid", k)
		}
		if err := k.Validate(); err == nil {
			t.Errorf("%q validate: expected error", k)
		}
	}
}

func TestMetaEverRevealed(t *testing.T) {
	if (Meta{}).EverRevealed() {
		t.Error("zero meta must read as never revealed")
	}
	if (Meta{RevealedBy: "alice@example.com", Revealed: "2026-07-14T00:00:00Z"}).EverRevealed() != true {
		t.Error("a meta with a revealer must read as revealed")
	}
}
