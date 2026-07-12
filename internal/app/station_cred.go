package app

import (
	"context"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/token"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// station credentials (ADR 0008): one credential per imaging station, bound to
// its station tag. A station-report authenticates the station it claims to be,
// so a leaked credential cannot report discoveries as another station. Shares
// the bound-credential machinery in cred.go; the token id is namespaced by
// "station-".

// StationCredentials issues and verifies per-station credentials over the
// token store.
type StationCredentials struct {
	store ports.TokenStore
	clock ports.Clock
}

// NewStationCredentials wires the service.
func NewStationCredentials(store ports.TokenStore, clock ports.Clock) *StationCredentials {
	return &StationCredentials{store: store, clock: clock}
}

// stationTokenID namespaces station credentials.
func stationTokenID(tag string) string { return "station-" + tag }

// Issue mints a fresh credential for a station, replacing any previous one.
// Returns the one-time secret; the station stores it out of the nix store and
// sends it as a bearer on every report.
func (s *StationCredentials) Issue(ctx context.Context, tag string) (string, error) {
	return issueBound(ctx, s.store, s.clock, token.Station, stationTokenID(tag), "station:"+tag, tag)
}

// Revoke removes a station's credential.
func (s *StationCredentials) Revoke(ctx context.Context, tag string) error {
	return s.store.Delete(ctx, stationTokenID(tag))
}

// AuthenticateTag verifies a credential AND that it belongs to the claimed
// station tag - the report path asserts the station is who it says it is.
func (s *StationCredentials) AuthenticateTag(ctx context.Context, secret, claimedTag string) bool {
	tag, ok := authenticateBound(ctx, s.store, s.clock, secret, token.Station)
	return ok && tag == claimedTag
}
