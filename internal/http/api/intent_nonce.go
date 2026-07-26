package api

import (
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strconv"
)

// intent_nonce.go: the wipe replay guard (design 0004). A wipe intent rides
// back on the check-in response with a server-signed nonce + timestamp; the
// device echoes both in its ack; the server verifies the ack carries a nonce
// IT issued recently. So a replayed or forged wipe ack cannot pass as a real
// one, and the guarantee is audit-visible - not merely a property of the
// TLS channel. Only the destructive wipe carries a nonce; lock/reboot/
// provision recur by design and stay unsigned.

// intentNonceWindow bounds how long a signed wipe instruction stays valid for
// its ack round-trip (design 0004: < 15 min).
const intentNonceWindow = 15 * 60 // seconds

// fleetIntentWipe mirrors fleet.IntentWipe. The device-facing check-in API
// stays decoupled from the fleet domain, so the one intent string it must
// recognise (the destructive wipe the replay guard signs) is a local const;
// a drift test in the fleet package pins them equal.
const fleetIntentWipe = "wipe"

// signIntentNonce is the stateless nonce: HMAC-SHA256 over the device tag, the
// intent and the issued-at second, keyed by the server's intent key. Stateless
// keeps it HA-safe - any replica verifies with the same key, no shared state.
func signIntentNonce(key []byte, tag, intent string, ts int64) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(tag))
	m.Write([]byte{0})
	m.Write([]byte(intent))
	m.Write([]byte{0})
	m.Write([]byte(strconv.FormatInt(ts, 10)))
	return hex.EncodeToString(m.Sum(nil))
}

// verifyIntentNonce reports whether nonce is a valid signature for (tag,
// intent, ts) AND the timestamp is within the window relative to now. Both
// checks are required: a matching signature on a stale timestamp is a replay.
func verifyIntentNonce(key []byte, tag, intent string, ts, now int64, nonce string) bool {
	if len(key) == 0 || nonce == "" || ts == 0 {
		return false
	}
	if now-ts < 0 || now-ts > intentNonceWindow {
		return false
	}
	want := signIntentNonce(key, tag, intent, ts)
	return subtle.ConstantTimeCompare([]byte(want), []byte(nonce)) == 1
}
