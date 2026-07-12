package app

import (
	"context"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// cred.go holds the machinery shared by the two bound-credential services
// (device and station, ADR 0008): a credential whose subject is fixed to the
// thing it authenticates, so it can only ever act as itself. The auth logic is
// security-critical and identical for both, so it lives here once rather than
// copied per service - fix a timing/enumeration bug in one place, not two.

// boundCredTTL is long: devices and stations are long-lived and re-issue on
// re-image rather than expiry.
const boundCredTTL = 5 * 365 * 24 * time.Hour

// issueBound mints a fresh credential of the given kind bound to subject,
// replacing any previous one (re-image rotates). id namespaces the kind so
// device/station/personal ids never collide. Returns the one-time secret.
func issueBound(ctx context.Context, store ports.TokenStore, clock ports.Clock,
	kind token.Kind, id, name, subject string) (string, error) {
	tok, secret, err := token.Mint(id, name, kind, subject, nil, "", clock.Now(), boundCredTTL)
	if err != nil {
		return "", err
	}
	if err := store.Put(ctx, tok); err != nil {
		return "", err
	}
	return secret, nil
}

// authenticateBound verifies a bound credential and returns the subject it
// proves. A store miss or wrong-kind token burns equal work (DummyVerify) so
// neither the id space nor the kind is a timing oracle.
func authenticateBound(ctx context.Context, store ports.TokenStore, clock ports.Clock,
	secret string, want token.Kind) (string, bool) {
	id := token.IDFromSecret(secret)
	if id == "" {
		return "", false
	}
	tok, ok, err := store.Get(ctx, id)
	if err != nil || !ok || tok.Kind != want {
		token.DummyVerify(secret)
		return "", false
	}
	now := clock.Now()
	if tok.Expired(now) || !tok.Verify(secret) {
		return "", false
	}
	_ = store.TouchLastUsed(ctx, id, now)
	return tok.Subject, true
}
