package app

import (
	"context"
	"fmt"
	"log/slog"

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

// ReconcileWithFleet revokes device credentials whose tag no longer exists in
// the fleet document, and reports how many it removed.
//
// WHY THIS EXISTS. Both removal paths revoke a credential as they go
// (web/device_ops.go, http/api/handlers.go), so this is not a patch over a
// forgotten call. The problem is that the credential's lifecycle hangs off
// those two handlers rather than off the document: a device that leaves
// fleet.json any other way - a change request, a commit in the overlay, a
// restore from backup - leaves its credential behind, and nothing notices.
//
// Measured in production on 2026-08-06: 15 live device credentials against 2
// devices in the fleet. The orphans were minted on 2026-07-13 and would have
// stayed valid until 2031 (boundCredTTL is five years, cred.go). Each one
// authenticates check-ins for a tag the fleet does not have, which is exactly
// what kept putting removed machines back on the overview.
//
// It is the same shape as two other drifts found the same day - the observed
// plane and git both held records the config plane had dropped - and the same
// remedy: sweep at startup, say so out loud.
//
// REFUSES TO RUN ON AN EMPTY FLEET. A document that failed to load, or a
// restore that has not finished, would otherwise revoke every credential in
// the fleet. Leaving orphans is a small problem; locking out every device is a
// large one, so the ambiguous case does nothing and says why.
func (d *DeviceCredentials) ReconcileWithFleet(ctx context.Context, known map[string]bool) (int, error) {
	if len(known) == 0 {
		slog.Warn("device credentials: refusing to reconcile against an empty fleet document")
		return 0, nil
	}
	toks, err := d.store.ListByKind(ctx, token.Device)
	if err != nil {
		return 0, fmt.Errorf("reconcile device credentials: %w", err)
	}
	revoked := 0
	for _, t := range toks {
		// Subject IS the bare tag (cred.go: Mint takes name "device:<tag>"
		// and subject "<tag>"), and it is what Authenticate returns - so it is
		// what has to match the document. Matching on the id would work too
		// and would couple this to the "device-" prefix twice over.
		tag := t.Subject
		if known[tag] {
			continue
		}
		if err := d.store.Delete(ctx, t.ID); err != nil {
			slog.Warn("device credentials: orphan not revoked", "id", t.ID, "tag", tag, "err", err)
			continue
		}
		slog.Info("device credentials: revoked an orphan", "id", t.ID, "tag", tag,
			"reason", "tag is not in the fleet document")
		revoked++
	}
	return revoked, nil
}
