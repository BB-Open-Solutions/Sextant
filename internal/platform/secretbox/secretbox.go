// Package secretbox is Sextant's authenticated-encryption helper for the
// secret values it must hold at rest. AES-256-GCM, random nonce per message,
// keys supplied out of band (env).
//
// What it protects, in rising order of how badly you want it back:
//   - an operator-entered SMTP password (app/mail.go)
//   - device diagnostics bundles, 14-day retention (app/diagnostics.go)
//   - LUKS RECOVERY KEYS escrowed from devices (app/device_secrets.go, design
//     0009). Losing these means losing the ability to unlock a locked machine.
//
// That last one is why this package supports KEY ROTATION rather than holding
// a single key. It used to store bare nonce||ciphertext with no key
// identifier, so rotating SEXTANT_SECRET_KEY made every stored value
// permanently unreadable - the escrowed recovery keys included. An operator who
// rotated after a suspected leak, which is exactly when they would, would have
// destroyed their own break-glass path. Sealed values now name the key that
// sealed them, several keys can be configured at once, and rotation is
// therefore additive: put the new key first, keep the old one, and everything
// already stored keeps opening.
//
// Sextant's default posture is still to hold only secret REFERENCES; this
// exists for the paths where a value must survive in Postgres.
package secretbox

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrDisabled is returned when no key is configured. The caller treats this as
// "the encrypted-value path is unavailable" and falls back to a secret
// reference - fail-closed, never plaintext.
var ErrDisabled = errors.New("secretbox: no encryption key configured")

// magic marks the versioned envelope. Four bytes, so a legacy blob (which
// begins with a random nonce) is astronomically unlikely to be mistaken for
// one - and Open falls back to the legacy layout if the envelope does not
// parse or does not open, so even that collision costs only a wasted attempt.
var magic = []byte("SXB1")

// keyIDLen is how many bytes of the key's SHA-256 identify it. The id is
// DERIVED from the key material rather than named by the operator: rotation is
// then just "add the new key to the list", with no bookkeeping to get wrong,
// and no name to accidentally reuse for different bytes. Four bytes is ample
// to tell a handful of keys apart, and the id is not a secret - it identifies,
// it does not authenticate (GCM does that).
const keyIDLen = 4

type key struct {
	id   []byte
	aead cipher.AEAD
}

// Sealer seals with its primary key and opens with any configured key.
// The zero Sealer is disabled: every operation returns ErrDisabled, so a
// deployment that sets no key simply cannot store entered secrets.
type Sealer struct {
	primary *key
	// keys is every configured key, primary first, for opening.
	keys []*key
}

// New builds a Sealer from one or more base64-encoded 32-byte keys, separated
// by commas or whitespace. The FIRST key seals; every key may open. An empty
// value yields a disabled Sealer (not an error): the encrypted path is
// optional. A non-empty but malformed key IS an error - a misconfigured key
// must not boot silently.
//
// To rotate: prepend the new key, keep the old one, restart. New writes use
// the new key; existing values keep opening with the old one. Drop the old key
// only once nothing sealed under it remains - until a re-seal pass exists,
// that means keeping it.
func New(b64keys string) (Sealer, error) {
	fields := SplitKeys(b64keys)
	if len(fields) == 0 {
		return Sealer{}, nil
	}
	var s Sealer
	seen := map[string]bool{}
	for i, f := range fields {
		k, err := newKey(f)
		if err != nil {
			return Sealer{}, fmt.Errorf("secretbox: key %d: %w", i+1, err)
		}
		// A duplicate would make the list lie about how many keys are live,
		// and after a rotation that is precisely the thing an operator checks.
		id := hex.EncodeToString(k.id)
		if seen[id] {
			return Sealer{}, fmt.Errorf("secretbox: key %d (%s) is a duplicate of an earlier key", i+1, id)
		}
		seen[id] = true
		s.keys = append(s.keys, k)
	}
	s.primary = s.keys[0]
	return s, nil
}

// SplitKeys parses a configured key list into its individual base64 keys, the
// primary first. Exported because SEXTANT_SECRET_KEY has a second consumer:
// the wipe replay-nonce key is derived from it (domain-separated) and must
// derive from the PRIMARY key alone. Letting that consumer base64-decode the
// whole value would decode a comma-separated list to nothing and silently turn
// the replay guard off - a rotation would have disabled a destructive-action
// safeguard without a word. One parser, one meaning.
func SplitKeys(b64keys string) []string {
	return strings.FieldsFunc(b64keys, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	})
}

func newKey(b64key string) (*key, error) {
	raw, err := base64.StdEncoding.DecodeString(b64key)
	if err != nil {
		return nil, fmt.Errorf("not valid base64: %w", err)
	}
	if len(raw) != 32 {
		return nil, fmt.Errorf("must be 32 bytes (got %d)", len(raw))
	}
	block, err := aes.NewCipher(raw)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(raw)
	return &key{id: sum[:keyIDLen], aead: aead}, nil
}

// Enabled reports whether a key is configured.
func (s Sealer) Enabled() bool { return s.primary != nil }

// PrimaryKeyID is the identifier the next Seal will stamp, for logging and for
// an operator verifying that a rotation actually took effect. Empty when
// disabled. Safe to log: it identifies a key, it does not authenticate one.
func (s Sealer) PrimaryKeyID() string {
	if s.primary == nil {
		return ""
	}
	return hex.EncodeToString(s.primary.id)
}

// KeyCount is how many keys can open values, primary included.
func (s Sealer) KeyCount() int { return len(s.keys) }

// Seal encrypts plaintext with the primary key and returns
// magic||keyID||nonce||ciphertext. The nonce is random per call, so sealing
// the same value twice yields different bytes.
func (s Sealer) Seal(plaintext []byte) ([]byte, error) {
	if s.primary == nil {
		return nil, ErrDisabled
	}
	k := s.primary
	nonce := make([]byte, k.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	out := make([]byte, 0, len(magic)+keyIDLen+len(nonce)+len(plaintext)+k.aead.Overhead())
	out = append(out, magic...)
	out = append(out, k.id...)
	out = append(out, nonce...)
	return k.aead.Seal(out, nonce, plaintext, nil), nil
}

// Open reverses Seal, authenticating the ciphertext. A wrong key or tampered
// bytes return an error, never partial plaintext.
//
// Two layouts are accepted. A value carrying the envelope names its key, so a
// missing key is reported as exactly that rather than as generic corruption -
// the difference between "restore the old key" and "this data is gone". A
// value without the envelope predates key ids; every configured key is tried,
// because after a rotation the one that sealed it is no longer the primary.
func (s Sealer) Open(sealed []byte) ([]byte, error) {
	if s.primary == nil {
		return nil, ErrDisabled
	}
	if k, nonce, ct, ok := s.parseEnvelope(sealed); ok {
		pt, err := k.aead.Open(nil, nonce, ct, nil)
		if err != nil {
			return nil, fmt.Errorf("secretbox: open with key %s: %w", hex.EncodeToString(k.id), err)
		}
		return pt, nil
	}
	// Unknown key id in a well-formed envelope: say which one, so an operator
	// can put it back instead of concluding the data is unrecoverable.
	if id, ok := s.envelopeKeyID(sealed); ok {
		return nil, fmt.Errorf("secretbox: sealed with key %s, which is not configured (add it to SEXTANT_SECRET_KEY to read this value)", id)
	}
	return s.openLegacy(sealed)
}

// envelopeKeyID reports the key id of a well-formed envelope, whether or not
// that key is configured.
func (s Sealer) envelopeKeyID(sealed []byte) (string, bool) {
	if len(sealed) < len(magic)+keyIDLen || !bytes.Equal(sealed[:len(magic)], magic) {
		return "", false
	}
	return hex.EncodeToString(sealed[len(magic) : len(magic)+keyIDLen]), true
}

// parseEnvelope splits a versioned value and resolves its key. ok is false
// when the value is not an envelope, names an unconfigured key, or is too
// short to hold a nonce.
func (s Sealer) parseEnvelope(sealed []byte) (k *key, nonce, ct []byte, ok bool) {
	head := len(magic) + keyIDLen
	if len(sealed) < head || !bytes.Equal(sealed[:len(magic)], magic) {
		return nil, nil, nil, false
	}
	id := sealed[len(magic):head]
	for _, cand := range s.keys {
		if bytes.Equal(cand.id, id) {
			k = cand
			break
		}
	}
	if k == nil {
		return nil, nil, nil, false
	}
	n := k.aead.NonceSize()
	if len(sealed) < head+n {
		return nil, nil, nil, false
	}
	return k, sealed[head : head+n], sealed[head+n:], true
}

// openLegacy reads the pre-rotation layout (bare nonce||ciphertext) by trying
// every configured key. GCM authenticates, so a wrong key cannot yield wrong
// plaintext - only a failure.
func (s Sealer) openLegacy(sealed []byte) ([]byte, error) {
	n := s.primary.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := sealed[:n], sealed[n:]
	for _, k := range s.keys {
		if pt, err := k.aead.Open(nil, nonce, ct, nil); err == nil {
			return pt, nil
		}
	}
	return nil, errors.New("secretbox: open: no configured key opens this value")
}
