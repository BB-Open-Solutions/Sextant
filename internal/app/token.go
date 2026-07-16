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
	// dir, when set, prunes a token's group snapshot to groups that still
	// exist in the directory at authentication time (see Authenticate).
	dir ports.Directory
}

// NewTokenService wires the service. The default TTL is deliberately 30
// days, not 90: a personal token's group snapshot is only as fresh as its
// lifetime, and a shorter ceiling bounds how long removed rights can
// survive (ISO 27001 A.9.2.6). Callers pass an explicit TTL to deviate.
func NewTokenService(store ports.TokenStore, clock ports.Clock, defaultTTL time.Duration) *TokenService {
	if defaultTTL <= 0 {
		defaultTTL = 30 * 24 * time.Hour
	}
	return &TokenService{store: store, clock: clock, DefaultTTL: defaultTTL}
}

// WithDirectory enables snapshot pruning against the (cached) directory.
// Returns the service for chaining at wiring time.
func (s *TokenService) WithDirectory(d ports.Directory) *TokenService {
	s.dir = d
	return s
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

// ListServiceAccounts returns every service-account token across all
// subjects. Owner-only in the transport layer; never called on the auth
// path (it scans by kind, not by the id embedded in a secret).
func (s *TokenService) ListServiceAccounts(ctx context.Context) ([]token.Token, error) {
	return s.store.ListByKind(ctx, token.Service)
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
//
// A store miss runs a dummy verify so response time does not reveal
// whether a well-formed token id exists (no enumeration oracle).
func (s *TokenService) Authenticate(ctx context.Context, secret string) (identity.User, identity.Role, bool) {
	id := token.IDFromSecret(secret)
	if id == "" {
		return identity.User{}, identity.None, false
	}
	tok, ok, err := s.store.Get(ctx, id)
	if err != nil || !ok {
		token.DummyVerify(secret) // equalize timing with the hit path
		return identity.User{}, identity.None, false
	}
	// Bound machine credentials share the store but must never authenticate the
	// operator API. Device credentials belong to the check-in path (ADR 0008);
	// station credentials may only submit discoveries. Reject both kinds here so
	// a leaked device/station secret can never reach an operator endpoint - the
	// group-based authz layer downstream is defence in depth, not the only wall.
	if tok.Kind == token.Device || tok.Kind == token.Station {
		token.DummyVerify(secret)
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
	u := tok.User()
	// The group snapshot is as old as the token. A directory cannot tell us
	// per-user membership (no such port surface), but it CAN tell us which
	// groups still exist: prune snapshot groups that were deleted from the
	// directory, so rights tied to a removed group die immediately instead
	// of living out the token's TTL. Membership-removal-while-group-exists
	// remains bounded by the (30-day) TTL until a per-user membership
	// adapter exists - documented residual risk (ADR 0008). Best effort: an
	// unreachable directory must not lock every API client out, so on error
	// the snapshot stands.
	if s.dir != nil && len(u.Groups) > 0 {
		if existing, err := s.dir.ListGroups(ctx, ""); err == nil {
			known := make(map[string]bool, len(existing))
			for _, g := range existing {
				known[g.Name] = true
			}
			kept := u.Groups[:0]
			for _, g := range u.Groups {
				if known[g] {
					kept = append(kept, g)
				}
			}
			u.Groups = kept
		}
	}
	return u, ceiling, true
}
