package app

import (
	"context"
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// device credentials (ADR 0008): one credential per device, bound to its
// tag. A check-in authenticates the device it claims to be, so a leaked
// credential cannot impersonate another device. Reuses the token
// machinery (hashing, expiry, store); the token id is the device tag.

// deviceCredTTL is long: devices are long-lived and re-issue on re-image.
const deviceCredTTL = 5 * 365 * 24 * time.Hour

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
// personal/service token ids.
func deviceTokenID(tag string) string { return "device-" + tag }

// Issue mints a fresh credential for a device, replacing any previous one
// (re-image rotates). Returns the one-time secret.
func (d *DeviceCredentials) Issue(ctx context.Context, tag string) (string, error) {
	id := deviceTokenID(tag)
	tok, secret, err := token.Mint(id, "device:"+tag, token.Device, tag,
		nil, "", d.clock.Now(), deviceCredTTL)
	if err != nil {
		return "", err
	}
	if err := d.store.Put(ctx, tok); err != nil {
		return "", err
	}
	return secret, nil
}

// Revoke removes a device's credential (retire / unenroll).
func (d *DeviceCredentials) Revoke(ctx context.Context, tag string) error {
	return d.store.Delete(ctx, deviceTokenID(tag))
}

// Authenticate verifies a device credential and returns the tag it proves.
// A store miss burns equal work (no enumeration oracle).
func (d *DeviceCredentials) Authenticate(ctx context.Context, secret string) (string, bool) {
	id := token.IDFromSecret(secret)
	if id == "" {
		return "", false
	}
	tok, ok, err := d.store.Get(ctx, id)
	if err != nil || !ok {
		token.DummyVerify(secret)
		return "", false
	}
	if tok.Kind != token.Device {
		token.DummyVerify(secret)
		return "", false // a personal/service token is not a device credential
	}
	now := d.clock.Now()
	if tok.Expired(now) || !tok.Verify(secret) {
		return "", false
	}
	_ = d.store.TouchLastUsed(ctx, id, now)
	return tok.Subject, true // the device tag
}

// AuthenticateTag verifies a credential AND that it belongs to the claimed
// tag - the check-in path asserts the device is who it says it is.
func (d *DeviceCredentials) AuthenticateTag(ctx context.Context, secret, claimedTag string) bool {
	tag, ok := d.Authenticate(ctx, secret)
	if !ok {
		return false
	}
	if tag != claimedTag {
		return false // credential is valid but for a different device
	}
	return true
}

var _ = fmt.Sprint
