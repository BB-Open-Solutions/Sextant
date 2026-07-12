package ports

import (
	"context"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
)

// TokenStore persists API credentials (ADR 0008). Secrets are never
// stored - the domain hashes them; the store keeps records by id.
type TokenStore interface {
	// Put creates or replaces a token record.
	Put(ctx context.Context, t token.Token) error
	// Get returns a token by id.
	Get(ctx context.Context, id string) (token.Token, bool, error)
	// ListBySubject returns a principal's tokens (for self-management).
	ListBySubject(ctx context.Context, subject string) ([]token.Token, error)
	// ListByKind returns every token of one kind across all subjects (for the
	// owner-only service-account admin view). Never call this on the auth path.
	ListByKind(ctx context.Context, kind token.Kind) ([]token.Token, error)
	// Delete revokes a token by id.
	Delete(ctx context.Context, id string) error
	// TouchLastUsed records a use; best-effort, must not block auth.
	TouchLastUsed(ctx context.Context, id string, at time.Time) error
}
