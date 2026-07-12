package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
)

// StationAuthenticator verifies a per-station credential against a claimed
// station tag (ADR 0008). Implemented by app.StationCredentials.
type StationAuthenticator interface {
	AuthenticateTag(ctx context.Context, secret, claimedTag string) bool
}

// StationAPI serves the imaging-station report endpoint. A station reports the
// devices it has seen over PXE; the operator later enrolls them. Auth prefers a
// per-station credential (the station proves it is the tag it reports); a
// shared bridge token is accepted only as a labelled migration path, exactly
// like the check-in endpoint.
type StationAPI struct {
	svc      *app.DiscoveryService
	stations StationAuthenticator
	shared   string // shared bridge token; "" disables it
	log      *slog.Logger
}

// NewStation builds the station-report surface. At least one auth source must
// be set or the endpoint is disabled (fail-closed).
func NewStation(svc *app.DiscoveryService, stations StationAuthenticator, sharedToken string, log *slog.Logger) *StationAPI {
	return &StationAPI{svc: svc, stations: stations, shared: sharedToken, log: log}
}

// Routes registers the station-facing endpoint.
func (s *StationAPI) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/station/{tag}/report", s.handleReport)
}

func (s *StationAPI) handleReport(w http.ResponseWriter, r *http.Request) {
	if s.stations == nil && s.shared == "" {
		http.Error(w, "station reporting disabled: no station auth configured", http.StatusForbidden)
		return
	}
	station := r.PathValue("tag")
	if station == "" {
		http.Error(w, "station tag required", http.StatusBadRequest)
		return
	}

	var report discovery.Report
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&report); err != nil {
		http.Error(w, "bad report body: "+err.Error(), http.StatusBadRequest)
		return
	}

	secret := bearerToken(r)
	if !s.authorized(r, secret, station) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-station"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := s.svc.Report(r.Context(), station, report); err != nil {
		// A validation error is the station's fault (bad MAC, oversized
		// batch); log it so a misbehaving station is visible, and 400.
		s.log.Warn("station report rejected", "station", station, "devices", len(report.Devices), "err", err)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	s.log.Info("station report accepted", "station", station, "devices", len(report.Devices))
	w.WriteHeader(http.StatusNoContent)
}

// authorized accepts a per-station credential bound to the reported station
// first, then the shared bridge token. A station credential for a DIFFERENT
// station is rejected even though it is otherwise valid.
func (s *StationAPI) authorized(r *http.Request, secret, station string) bool {
	if s.stations != nil && s.stations.AuthenticateTag(r.Context(), secret, station) {
		return true
	}
	if s.shared != "" && subtle.ConstantTimeCompare([]byte(secret), []byte(s.shared)) == 1 {
		return true
	}
	return false
}
