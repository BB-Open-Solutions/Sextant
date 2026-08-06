package ports

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/forge"
)

// ForgeIdentityStore persists the console's own forge credential, one per
// tenant (ADR 0022). The stored token is a sealed blob; the store keeps it
// opaque and never logs it.
type ForgeIdentityStore interface {
	GetForgeIdentity(ctx context.Context, tenant string) (forge.Identity, bool, error)
	PutForgeIdentity(ctx context.Context, tenant string, id forge.Identity) error
	DeleteForgeIdentity(ctx context.Context, tenant string) error
}
