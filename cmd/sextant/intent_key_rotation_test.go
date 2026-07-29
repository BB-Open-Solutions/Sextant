package main

import (
	"bytes"
	"encoding/base64"
	"testing"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/platform/secretbox"
)

func b64key(seed byte) string {
	raw := make([]byte, 32)
	for i := range raw {
		raw[i] = seed ^ byte(i)
	}
	return base64.StdEncoding.EncodeToString(raw)
}

// intentKeyFor mirrors the wiring in capabilities.go: the replay-nonce key is
// derived from the PRIMARY key of SEXTANT_SECRET_KEY, not from the raw value.
func intentKeyFor(secretKey string) []byte {
	if keys := secretbox.SplitKeys(secretKey); len(keys) > 0 {
		return deriveIntentKey(keys[0])
	}
	return nil
}

// SEXTANT_SECRET_KEY can hold several keys so a rotation does not orphan
// already-sealed values. It has a SECOND consumer: the wipe replay-nonce key
// (design 0004). Deriving that from the raw value would base64-decode a
// comma-separated list to nothing and return nil - silently disabling the
// replay guard on the most destructive action there is, at the exact moment an
// operator rotated a key. This pins the two properties that prevent it.
func TestIntentNonceKeySurvivesKeyRotation(t *testing.T) {
	primary, retained := b64key(0xa1), b64key(0xb2)

	single := intentKeyFor(primary)
	if len(single) == 0 {
		t.Fatal("no intent nonce key derived from a valid single key")
	}

	// Retaining an old key for reading must not disturb the nonce key: the
	// guard has to keep verifying nonces issued before the rotation.
	for _, list := range []string{
		primary + "," + retained,
		primary + " " + retained,
		primary + ",\n" + retained,
	} {
		got := intentKeyFor(list)
		if len(got) == 0 {
			t.Fatalf("key list %q derived NO intent nonce key: the wipe replay guard would be off", list)
		}
		if !bytes.Equal(got, single) {
			t.Fatalf("key list %q changed the intent nonce key; nonces issued before the rotation would stop verifying", list)
		}
	}

	// Promoting a different key to primary SHOULD change it - the nonce key
	// follows the primary, so this is the deliberate half of the behaviour.
	if bytes.Equal(intentKeyFor(retained+","+primary), single) {
		t.Fatal("swapping the primary key left the intent nonce key unchanged")
	}
}

func TestIntentNonceKeyStaysOffWithoutAKey(t *testing.T) {
	for _, in := range []string{"", "   ", ","} {
		if got := intentKeyFor(in); got != nil {
			t.Fatalf("secret key %q produced an intent nonce key; the guard must stay off by construction", in)
		}
	}
}
