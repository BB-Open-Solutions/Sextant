package app

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// TokenService manages API credentials (ADR 0008) and authenticates them.
// It is the one place a token becomes a principal; the same resolver then
// judges that principal as it would a session.
type TokenService struct {
	store ports.TokenStore
	clock ports.Clock
	// DefaultTTL bounds new tokens and the personal-token group snapshot.
	DefaultTTL time.Duration
}

// NewTokenService wires the service.
func NewTokenService(store ports.TokenStore, clock ports.Clock, defaultTTL time.Duration) *TokenService {
	if defaultTTL <= 0 {
		defaultTTL = 90 * 24 * time.Hour
	}
	return &TokenService{store: store, clock: clock, DefaultTTL: defaultTTL}
}

// MintRequest describes a new token.
type MintRequest struct {
	ID      string
	Name    string
	Kind    token.Kind
	Subject string
	Groups  []string
	Ceiling string
	TTL     time.Duration
}

// Mint creates a token and returns the one-time secret. The caller has
// already authorized the request (self for personal, owner for service).
func (s *TokenService) Mint(ctx context.Context, req MintRequest) (token.Token, string, error) {
	if _, exists, err := s.store.Get(ctx, req.ID); err != nil {
		return token.Token{}, "", err
	} else if exists {
		return token.Token{}, "", fmt.Errorf("token id %q already exists", req.ID)
	}
	ttl := req.TTL
	if ttl <= 0 {
		ttl = s.DefaultTTL
	}
	tok, secret, err := token.Mint(req.ID, req.Name, req.Kind, req.Subject,
		req.Groups, req.Ceiling, s.clock.Now(), ttl)
	if err != nil {
		return token.Token{}, "", err
	}
	if err := s.store.Put(ctx, tok); err != nil {
		return token.Token{}, "", err
	}
	return tok, secret, nil
}

// List returns a principal's own tokens (never their secrets).
func (s *TokenService) List(ctx context.Context, subject string) ([]token.Token, error) {
	return s.store.ListBySubject(ctx, subject)
}

// Revoke deletes a token. The caller authorizes (owner of the token, or an
// org owner).
func (s *TokenService) Revoke(ctx context.Context, id string) error {
	if _, ok, err := s.store.Get(ctx, id); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("unknown token %q", id)
	}
	return s.store.Delete(ctx, id)
}

// Authenticate resolves a bearer secret to a principal, or false. It looks
// up exactly one record by the id embedded in the secret, verifies the
// hash, checks expiry, and records last-used best-effort. The returned
// user carries an optional ceiling the caller applies through the
// resolver - never widening the owner's rights.
func (s *TokenService) Authenticate(ctx context.Context, secret string) (identity.User, identity.Role, bool) {
	id := token.IDFromSecret(secret)
	if id == "" {
		return identity.User{}, identity.None, false
	}
	tok, ok, err := s.store.Get(ctx, id)
	if err != nil || !ok {
		return identity.User{}, identity.None, false
	}
	now := s.clock.Now()
	if tok.Expired(now) || !tok.Verify(secret) {
		return identity.User{}, identity.None, false
	}
	// Best-effort; a failure here must never deny a valid token.
	_ = s.store.TouchLastUsed(ctx, id, now)

	ceiling := identity.None
	if r, hasCeiling := tok.CeilingRole(); hasCeiling {
		ceiling = r
	}
	return tok.User(), ceiling, true
}
