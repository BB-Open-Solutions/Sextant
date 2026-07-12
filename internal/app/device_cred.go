package app

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// device credentials (ADR 0008): one credential per device, bound to its
// tag. A check-in authenticates the device it claims to be, so a leaked
// credential cannot impersonate another device. Shared bound-credential
// machinery lives in cred.go; the token id is namespaced by "device-".

// DeviceCredentials issues and verifies per-device credentials over the
// token store.
type DeviceCredentials struct {
	store ports.TokenStore
	clock ports.Clock
}

// NewDeviceCredentials wires the service.
func NewDeviceCredentials(store ports.TokenStore, clock ports.Clock) *DeviceCredentials {
	return &DeviceCredentials{store: store, clock: clock}
}

// deviceTokenID namespaces device credentials so they never collide with
// personal/service/station token ids.
func deviceTokenID(tag string) string { return "device-" + tag }

// Issue mints a fresh credential for a device, replacing any previous one
// (re-image rotates). Returns the one-time secret.
func (d *DeviceCredentials) Issue(ctx context.Context, tag string) (string, error) {
	return issueBound(ctx, d.store, d.clock, token.Device, deviceTokenID(tag), "device:"+tag, tag)
}

// Revoke removes a device's credential (retire / unenroll).
func (d *DeviceCredentials) Revoke(ctx context.Context, tag string) error {
	return d.store.Delete(ctx, deviceTokenID(tag))
}

// Authenticate verifies a device credential and returns the tag it proves.
func (d *DeviceCredentials) Authenticate(ctx context.Context, secret string) (string, bool) {
	return authenticateBound(ctx, d.store, d.clock, secret, token.Device)
}

// AuthenticateTag verifies a credential AND that it belongs to the claimed
// tag - the check-in path asserts the device is who it says it is.
func (d *DeviceCredentials) AuthenticateTag(ctx context.Context, secret, claimedTag string) bool {
	tag, ok := d.Authenticate(ctx, secret)
	return ok && tag == claimedTag
}
