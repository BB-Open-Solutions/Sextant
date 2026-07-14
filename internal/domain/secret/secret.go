// Package secret is the pure domain of per-device secrets held by Sextant:
// the material provisioning produces that must survive but stays confidential -
// a device's LUKS recovery passphrase, its break-glass local-admin password.
// The plaintext never lives here; the domain only names the kinds and carries
// the non-secret metadata (who created a secret, whether it has been revealed)
// that the store persists and the console shows. Encryption-at-rest and the
// RBAC-gated reveal live in the adapter and application layers.
package secret

import "fmt"

// Kind names what a stored secret is. The set is closed: an unknown kind is
// rejected before anything is sealed, so a typo cannot create an unmanaged
// secret class the reveal UI never surfaces.
type Kind string

const (
	// LUKS is a device's disk-encryption recovery passphrase. TPM2 enrolment
	// makes it unnecessary at boot; it remains as break-glass recovery.
	LUKS Kind = "luks"
	// Admin is a device's break-glass local administrator password.
	Admin Kind = "admin"
)

// Valid reports whether k is a known secret kind.
func (k Kind) Valid() bool {
	switch k {
	case LUKS, Admin:
		return true
	}
	return false
}

// Validate rejects an unknown kind.
func (k Kind) Validate() error {
	if !k.Valid() {
		return fmt.Errorf("unknown secret kind %q (luks|admin)", k)
	}
	return nil
}

// Meta is the non-secret record of one stored secret: enough to list and audit
// it without ever holding the plaintext. Times are RFC3339 in the store; the
// zero RevealedBy means it has never been revealed.
type Meta struct {
	Tag        string `json:"tag"`
	Kind       Kind   `json:"kind"`
	CreatedBy  string `json:"createdBy"`
	Created    string `json:"created"`
	RevealedBy string `json:"revealedBy,omitempty"`
	Revealed   string `json:"revealed,omitempty"`
}

// EverRevealed reports whether the secret has been read at least once - the
// one-shot signal the UI uses to warn that a value is no longer fresh.
func (m Meta) EverRevealed() bool { return m.RevealedBy != "" }
