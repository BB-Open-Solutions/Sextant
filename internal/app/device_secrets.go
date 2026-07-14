package app

import (
	"context"
	"fmt"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// DeviceSecretsService is the application surface of the per-device secret
// store: provisioning seals a secret at rest, and an authorized operator
// reveals it. The domain names the kinds; a ports.Sealer encrypts (in-process
// AES-256-GCM by default, an external key manager - OpenBao / Vault - as a
// drop-in); the store persists ciphertext only. RBAC is enforced by the caller
// (reveal is owner reach) - this service records who acted so every reveal is
// auditable.
type DeviceSecretsService struct {
	store  ports.DeviceSecretStore
	sealer ports.Sealer
	clock  ports.Clock
	tenant string
}

// NewDeviceSecretsService wires the per-device secret store for one tenant.
func NewDeviceSecretsService(store ports.DeviceSecretStore, sealer ports.Sealer, clock ports.Clock, tenant string) *DeviceSecretsService {
	if tenant == "" {
		tenant = DefaultTenant
	}
	return &DeviceSecretsService{store: store, sealer: sealer, clock: clock, tenant: tenant}
}

// Enabled reports whether a secret store and an encryption key are both present.
// A disabled service stores nothing (rather than persisting plaintext), so the
// caller can fall back or warn.
func (s *DeviceSecretsService) Enabled() bool {
	return s != nil && s.store != nil && s.sealer.Enabled()
}

// Store seals plaintext and persists it for a device+kind. It refuses an
// unknown kind, an empty value, and a disabled encryption key - never writing a
// secret it cannot protect.
func (s *DeviceSecretsService) Store(ctx context.Context, tag string, kind secret.Kind, plaintext, createdBy string) error {
	if err := kind.Validate(); err != nil {
		return err
	}
	if plaintext == "" {
		return fmt.Errorf("refusing to store an empty %s secret for %s", kind, tag)
	}
	if !s.sealer.Enabled() {
		return ports.ErrSealerDisabled
	}
	sealed, err := s.sealer.Seal([]byte(plaintext))
	if err != nil {
		return fmt.Errorf("seal %s secret for %s: %w", kind, tag, err)
	}
	return s.store.Put(ctx, s.tenant, tag, kind, sealed, createdBy, s.clock.Now())
}

// Reveal unseals a device's secret and stamps who read it. RBAC (owner) is the
// caller's responsibility; revealedBy is recorded for the audit trail. Returns
// ok=false when no such secret exists.
func (s *DeviceSecretsService) Reveal(ctx context.Context, tag string, kind secret.Kind, revealedBy string) (string, bool, error) {
	if err := kind.Validate(); err != nil {
		return "", false, err
	}
	sealed, _, ok, err := s.store.Get(ctx, s.tenant, tag, kind)
	if err != nil || !ok {
		return "", ok, err
	}
	plaintext, err := s.sealer.Open(sealed)
	if err != nil {
		return "", true, fmt.Errorf("open %s secret for %s: %w", kind, tag, err)
	}
	if err := s.store.MarkRevealed(ctx, s.tenant, tag, kind, revealedBy, s.clock.Now()); err != nil {
		return "", true, fmt.Errorf("record reveal of %s secret for %s: %w", kind, tag, err)
	}
	return string(plaintext), true, nil
}

// List returns the metadata of a device's secrets (which kinds exist, who
// created them, whether they have been revealed) - never any plaintext.
func (s *DeviceSecretsService) List(ctx context.Context, tag string) ([]secret.Meta, error) {
	return s.store.List(ctx, s.tenant, tag)
}
