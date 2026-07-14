package ports

import "errors"

// ErrSealerDisabled is returned (and expected) when no encryption key is
// configured: callers treat it as "secrets are not persisted", not a failure.
var ErrSealerDisabled = errors.New("ports: sealer disabled (no encryption key)")

// Sealer encrypts and decrypts small secrets at rest. It is the seam that lets
// the encryption backend be swapped without touching the domain or the
// application services: the default is in-process AES-256-GCM (secretbox), and
// an external key manager (OpenBao / HashiCorp Vault transit, where the key
// never leaves the vault) is a drop-in implementation of this same interface.
//
// A disabled sealer (no key configured) reports Enabled() == false and returns
// ErrSealerDisabled from Seal/Open, so a deployment without a key runs - secrets
// simply are not persisted (an operator copies a reveal-once value into a
// password manager) rather than being written in the clear.
type Sealer interface {
	// Enabled reports whether an encryption key is configured.
	Enabled() bool
	// Seal encrypts plaintext, returning an opaque token (nonce||ciphertext for
	// the AES backend) safe to store at rest.
	Seal(plaintext []byte) ([]byte, error)
	// Open decrypts what Seal produced, or errors (never partial plaintext).
	Open(sealed []byte) ([]byte, error)
}
