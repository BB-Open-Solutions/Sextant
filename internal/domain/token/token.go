// Package token is the pure domain of API credentials (ADR 0008): personal
// tokens that act AS their user, and service accounts with explicit
// bindings. The one authorization path is preserved - a token yields an
// identity.User which the same resolver judges as a session would. Secret
// generation and hashing are pure functions here; storage is a port.
package token

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"regexp"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// Kind distinguishes a personal token (acts as a human) from a service
// account (a named non-human principal with its own bindings).
type Kind string

const (
	// Personal tokens derive rights from their owner's current bindings.
	Personal Kind = "personal"
	// Service tokens carry explicit bindings in the access list.
	Service Kind = "service"
	// Device credentials authenticate one device to the check-in endpoint.
	// Subject is the device tag; a device can only ever be itself, closing
	// the shared-token impersonation gap (ADR 0008).
	Device Kind = "device"
	// Station credentials authenticate one imaging station to the
	// station-report endpoint. Subject is the station tag; a station can
	// only ever report as itself, same closed-gap model as Device. A
	// station may only submit discoveries - never console or API rights.
	Station Kind = "station"
)

// prefix identifies a Sextant token at a glance (and lets secret scanners
// match it). The random part follows.
const prefix = "sxt_"

// Token is the stored record. The plaintext secret exists only at creation
// time (returned once); only Hash is persisted.
type Token struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	// Subject is the owner (personal: the user's OIDC subject; service:
	// the service-account name, also its identity.User.Subject).
	Subject string `json:"subject"`
	// Groups snapshots the owner's IdP groups for a personal token, so
	// authorization works without a live IdP call; Expires bounds the
	// snapshot's staleness (ADR 0008).
	Groups []string `json:"groups,omitempty"`
	// Ceiling optionally narrows a personal token below its owner
	// (viewer|editor|owner); empty means no extra ceiling. It can only
	// reduce, never widen (enforced at resolution).
	Ceiling string `json:"ceiling,omitempty"`
	// Hash is the argon2id hash of the secret; the secret never persists.
	Hash string `json:"-"`

	Created  time.Time  `json:"created"`
	Expires  time.Time  `json:"expires"`
	LastUsed *time.Time `json:"lastUsed,omitempty"`
}

// argon2 parameters (OWASP-aligned defaults for interactive use).
const (
	argonTime    = 1
	argonMemory  = 64 * 1024
	argonThreads = 4
	argonKeyLen  = 32
	saltLen      = 16
	secretLen    = 24 // bytes of entropy in the token secret
)

// Mint generates a new token: a fresh secret, its hash, and the record.
// Returns the record and the ONE-TIME plaintext secret the caller shows
// the user and never stores. now/ttl are injected for determinism.
func Mint(id, name string, kind Kind, subject string, groups []string, ceiling string, now time.Time, ttl time.Duration) (Token, string, error) {
	if id == "" || name == "" || subject == "" {
		return Token{}, "", fmt.Errorf("token needs id, name and subject")
	}
	if kind != Personal && kind != Service && kind != Device && kind != Station {
		return Token{}, "", fmt.Errorf("unknown token kind %q", kind)
	}
	if ceiling != "" {
		if _, err := identity.ParseRole(ceiling); err != nil {
			return Token{}, "", fmt.Errorf("ceiling: %w", err)
		}
	}
	if ttl <= 0 {
		return Token{}, "", fmt.Errorf("token needs a positive ttl (no non-expiring tokens)")
	}
	if !idRe.MatchString(id) {
		return Token{}, "", fmt.Errorf("token id %q must be a lowercase slug", id)
	}
	secretRaw := make([]byte, secretLen)
	if _, err := rand.Read(secretRaw); err != nil {
		return Token{}, "", err
	}
	// Secret embeds the id (sxt_<id>_<random>) so verification looks up
	// exactly one record, then constant-time compares the hash - no full
	// table scan, no timing oracle over the id space.
	secret := prefix + id + "_" + base64.RawURLEncoding.EncodeToString(secretRaw)
	hash, err := hashSecret(secret)
	if err != nil {
		return Token{}, "", err
	}
	return Token{
		ID: id, Name: name, Kind: kind, Subject: subject,
		Groups: groups, Ceiling: ceiling, Hash: hash,
		Created: now, Expires: now.Add(ttl),
	}, secret, nil
}

// Expired reports whether the token is past its expiry.
func (t Token) Expired(now time.Time) bool { return !now.Before(t.Expires) }

// User projects the token onto the one authorization path: an
// identity.User the resolver judges exactly like a session. Personal
// tokens carry the owner's group snapshot; service tokens are principals
// whose bindings live in the access list.
func (t Token) User() identity.User {
	switch t.Kind {
	case Service:
		return identity.User{Subject: t.Subject, Name: t.Name, Groups: t.Groups}
	default:
		return identity.User{Subject: t.Subject, Name: t.Name, Groups: t.Groups}
	}
}

// CeilingRole returns the parsed ceiling and whether one is set.
func (t Token) CeilingRole() (identity.Role, bool) {
	if t.Ceiling == "" {
		return identity.None, false
	}
	r, _ := identity.ParseRole(t.Ceiling)
	return r, true
}

// Verify reports whether secret matches this token's hash (constant-time).
func (t Token) Verify(secret string) bool {
	want, err := decodeHash(t.Hash)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), want.salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, want.key) == 1
}

// dummyHash is a fixed argon2id hash used to burn the same work as a real
// verify when no token record exists, so authentication time does not
// reveal whether a token id is registered.
var dummyHash, _ = hashSecret("sxt_dummy_0000000000000000000000000000000000")

// DummyVerify runs one argon2 comparison against a fixed hash and discards
// the result. It exists solely to equalize the store-miss timing with the
// hit path; the return value is always false.
func DummyVerify(secret string) bool {
	d, err := decodeHash(dummyHash)
	if err != nil {
		return false
	}
	got := argon2.IDKey([]byte(secret), d.salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)
	return subtle.ConstantTimeCompare(got, d.key) == 1
}

// idRe constrains a token id (also a path/log-safe slug).
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// IDFromSecret extracts the token id embedded in a secret
// (sxt_<id>_<random>), or "" when the value is not a well-formed token.
func IDFromSecret(secret string) string {
	if !strings.HasPrefix(secret, prefix) {
		return ""
	}
	rest := secret[len(prefix):]
	i := strings.IndexByte(rest, '_')
	if i <= 0 {
		return ""
	}
	id := rest[:i]
	if !idRe.MatchString(id) {
		return ""
	}
	return id
}

// --- hashing (encoded string: argon2id$mem,time,threads$salt$key) ---

type decoded struct {
	salt []byte
	key  []byte
}

func hashSecret(secret string) (string, error) {
	salt := make([]byte, saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	key := argon2.IDKey([]byte(secret), salt,
		argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf("argon2id$%d,%d,%d$%s$%s",
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key)), nil
}

func decodeHash(s string) (decoded, error) {
	parts := strings.Split(s, "$")
	if len(parts) != 4 || parts[0] != "argon2id" {
		return decoded{}, fmt.Errorf("bad hash format")
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return decoded{}, err
	}
	key, err := base64.RawStdEncoding.DecodeString(parts[3])
	if err != nil {
		return decoded{}, err
	}
	return decoded{salt: salt, key: key}, nil
}
