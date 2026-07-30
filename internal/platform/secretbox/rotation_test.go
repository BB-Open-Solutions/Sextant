package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
)

func testKey(t *testing.T, seed byte) string {
	t.Helper()
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed ^ byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// The property that matters: rotating the key must not destroy what is already
// stored. Before key ids existed, a rotation made every sealed value
// permanently unreadable - including the escrowed LUKS recovery keys, i.e. the
// break-glass path for a locked laptop, discarded at exactly the moment an
// operator would rotate (a suspected leak).
func TestRotationKeepsOldValuesReadable(t *testing.T) {
	oldKey, newKey := testKey(t, 0x11), testKey(t, 0x22)

	before, err := New(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := before.Seal([]byte("recovery-phrase"))
	if err != nil {
		t.Fatal(err)
	}

	// Rotation: new key first, old key retained.
	after, err := New(newKey + "," + oldKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := after.Open(sealed)
	if err != nil {
		t.Fatalf("value sealed before the rotation no longer opens: %v", err)
	}
	if string(got) != "recovery-phrase" {
		t.Fatalf("got %q", got)
	}

	// And new writes use the new key, not the retained one.
	if after.PrimaryKeyID() == before.PrimaryKeyID() {
		t.Fatal("primary key id did not change after rotation")
	}
	fresh, err := after.Seal([]byte("x"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := before.Open(fresh); err == nil {
		t.Fatal("the pre-rotation sealer opened a value sealed with the new key")
	}
}

// Dropping the key that sealed a value must say SO, by name. The difference
// between "put key 3f2a back" and a generic authentication failure is the
// difference between recovering the data and concluding it is gone.
func TestOpenNamesTheMissingKey(t *testing.T) {
	sealer, err := New(testKey(t, 0x33))
	if err != nil {
		t.Fatal(err)
	}
	sealedID := sealer.PrimaryKeyID()
	sealed, err := sealer.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}

	other, err := New(testKey(t, 0x44))
	if err != nil {
		t.Fatal(err)
	}
	_, err = other.Open(sealed)
	if err == nil {
		t.Fatal("opened a value whose key is not configured")
	}
	if !strings.Contains(err.Error(), sealedID) {
		t.Fatalf("error does not name the missing key %s: %v", sealedID, err)
	}
	if !strings.Contains(err.Error(), "SEXTANT_SECRET_KEY") {
		t.Fatalf("error does not say how to fix it: %v", err)
	}
}

// Values written before this package had an envelope carry no key id. They must
// still open, or the fix for the rotation problem would itself be the data-loss
// event it was meant to prevent.
func TestOpensPreRotationLayout(t *testing.T) {
	b64 := testKey(t, 0x55)
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		t.Fatal(err)
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		t.Fatal(err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatal(err)
	}
	// Exactly what the old Seal produced: nonce||ciphertext, no header.
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatal(err)
	}
	legacy := aead.Seal(nonce, nonce, []byte("old-value"), nil)

	// Readable by the key alone, and still readable after a rotation that
	// keeps it.
	for name, keys := range map[string]string{
		"single key":        b64,
		"rotated, retained": testKey(t, 0x66) + " " + b64,
	} {
		s, err := New(keys)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		got, err := s.Open(legacy)
		if err != nil {
			t.Fatalf("%s: legacy value does not open: %v", name, err)
		}
		if string(got) != "old-value" {
			t.Fatalf("%s: got %q", name, got)
		}
	}
}

func TestNewRejectsDuplicateAndMalformedKeys(t *testing.T) {
	k := testKey(t, 0x77)
	if _, err := New(k + "," + k); err == nil {
		t.Fatal("accepted the same key twice; the key list would misreport how many keys are live")
	}
	if _, err := New(testKey(t, 0x88) + ",not-base64"); err == nil {
		t.Fatal("accepted a malformed second key")
	}
	if _, err := New(base64.StdEncoding.EncodeToString([]byte("too short"))); err == nil {
		t.Fatal("accepted a key that is not 32 bytes")
	}
}

func TestDisabledSealerStaysDisabled(t *testing.T) {
	for _, in := range []string{"", "   ", ",", " , "} {
		s, err := New(in)
		if err != nil {
			t.Fatalf("New(%q): %v", in, err)
		}
		if s.Enabled() {
			t.Fatalf("New(%q) produced an enabled sealer", in)
		}
		if _, err := s.Seal([]byte("x")); !errors.Is(err, ErrDisabled) {
			t.Fatalf("New(%q) Seal: want ErrDisabled, got %v", in, err)
		}
		if _, err := s.Open([]byte("x")); !errors.Is(err, ErrDisabled) {
			t.Fatalf("New(%q) Open: want ErrDisabled, got %v", in, err)
		}
	}
}

func TestTamperedValueFails(t *testing.T) {
	s, err := New(testKey(t, 0x99))
	if err != nil {
		t.Fatal(err)
	}
	sealed, err := s.Seal([]byte("secret"))
	if err != nil {
		t.Fatal(err)
	}
	sealed[len(sealed)-1] ^= 0xff
	if _, err := s.Open(sealed); err == nil {
		t.Fatal("opened a tampered value")
	}
}
