// Package secretbox is Sextant's small authenticated-encryption helper for the
// few secret values it must hold at rest (an operator-entered SMTP password).
// It is deliberately narrow: AES-256-GCM with a single symmetric key supplied
// out of band (env), random nonce per message. Sextant's default posture is to
// hold only secret REFERENCES; this exists for the opt-in path where a value
// is entered through the console and must survive in Postgres.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
)

// ErrDisabled is returned when no key is configured. The caller treats this as
// "the encrypted-value path is unavailable" and falls back to a secret
// reference - fail-closed, never plaintext.
var ErrDisabled = errors.New("secretbox: no encryption key configured")

// Sealer seals and opens secret values with one AES-256-GCM key. The zero
// Sealer is disabled: every operation returns ErrDisabled, so a deployment
// that sets no key simply cannot store entered secrets.
type Sealer struct {
	aead cipher.AEAD
}

// New builds a Sealer from a base64-encoded 32-byte key. An empty key yields a
// disabled Sealer (not an error): the encrypted path is optional. A non-empty
// but malformed key IS an error - a misconfigured key must not boot silently.
func New(b64key string) (Sealer, error) {
	if b64key == "" {
		return Sealer{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(b64key)
	if err != nil {
		return Sealer{}, fmt.Errorf("secretbox: key is not valid base64: %w", err)
	}
	if len(key) != 32 {
		return Sealer{}, fmt.Errorf("secretbox: key must be 32 bytes (got %d)", len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return Sealer{}, fmt.Errorf("secretbox: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return Sealer{}, fmt.Errorf("secretbox: %w", err)
	}
	return Sealer{aead: aead}, nil
}

// Enabled reports whether a key is configured.
func (s Sealer) Enabled() bool { return s.aead != nil }

// Seal encrypts plaintext and returns nonce||ciphertext. The nonce is random
// per call, so sealing the same value twice yields different bytes.
func (s Sealer) Seal(plaintext []byte) ([]byte, error) {
	if s.aead == nil {
		return nil, ErrDisabled
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("secretbox: nonce: %w", err)
	}
	return s.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Open reverses Seal, authenticating the ciphertext. A wrong key or tampered
// bytes return an error, never partial plaintext.
func (s Sealer) Open(sealed []byte) ([]byte, error) {
	if s.aead == nil {
		return nil, ErrDisabled
	}
	n := s.aead.NonceSize()
	if len(sealed) < n {
		return nil, errors.New("secretbox: ciphertext too short")
	}
	nonce, ct := sealed[:n], sealed[n:]
	pt, err := s.aead.Open(nil, nonce, ct, nil)
	if err != nil {
		return nil, fmt.Errorf("secretbox: open: %w", err)
	}
	return pt, nil
}
