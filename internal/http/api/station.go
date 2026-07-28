package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/discovery"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/imaging"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

// secretSink seals a per-device secret. Optional on the station API: nil or a
// disabled sink just skips sealing (and the key is not persisted, logged loudly).
// Implemented by app.DeviceSecretsService.
type secretSink interface {
	Enabled() bool
	Store(ctx context.Context, tag string, kind secret.Kind, plaintext, createdBy string) error
}

// StationAuthenticator verifies a per-station credential against a claimed
// station tag (ADR 0008). Implemented by app.StationCredentials.
type StationAuthenticator interface {
	AuthenticateTag(ctx context.Context, secret, claimedTag string) bool
}

// deviceCredIssuer mints a device's one-time agent credential. Implemented by
// app.DeviceCredentials; kept as an interface so the station API depends only
// on what it uses.
type deviceCredIssuer interface {
	Issue(ctx context.Context, tag string) (string, error)
}

// StationAPI serves the imaging-station endpoints. A station reports the
// devices it has seen over PXE (discovery), claims the image jobs an operator
// dispatched, and reports install progress. Auth prefers a per-station
// credential (the station proves it is the tag it reports); a shared bridge
// token is accepted only as a labelled migration path, like the check-in
// endpoint.
type StationAPI struct {
	svc      *app.DiscoveryService
	imaging  *app.ImagingService
	devCreds deviceCredIssuer
	secrets  secretSink // optional: seals a reported LUKS key at install
	stations StationAuthenticator
	shared   string // shared bridge token; "" disables it
	log      *slog.Logger
}

// WithSecrets wires the per-device secret store so an installed device's LUKS
// recovery key is sealed at rest instead of kept in the job message. Returns
// the receiver for chaining at construction.
func (s *StationAPI) WithSecrets(sink secretSink) *StationAPI {
	s.secrets = sink
	return s
}

// NewStation builds the station-report surface. At least one auth source must
// be set or the endpoint is disabled (fail-closed). imaging/devCreds may be
// nil (the job endpoints then report as unavailable).
func NewStation(svc *app.DiscoveryService, imaging *app.ImagingService, devCreds deviceCredIssuer, stations StationAuthenticator, sharedToken string, log *slog.Logger) *StationAPI {
	return &StationAPI{svc: svc, imaging: imaging, devCreds: devCreds, stations: stations, shared: sharedToken, log: log}
}

// Routes registers the station-facing endpoints.
func (s *StationAPI) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/station/{tag}/report", s.handleReport)
	mux.HandleFunc("GET /api/station/{tag}/jobs", s.handleJobs)
	mux.HandleFunc("POST /api/station/{tag}/jobs/claim", s.handleClaim)
	mux.HandleFunc("POST /api/station/{tag}/jobs/{mac}/status", s.handleJobStatus)
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

	// Authenticate before reading/decoding the body: the station tag is
	// already known from the path, so a bad credential is rejected without
	// paying for a (bounded but still costly) JSON parse of the report.
	secret := bearerToken(r)
	if !s.authorized(r, secret, station) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-station"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var report discovery.Report
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<20))
	if err := dec.Decode(&report); err != nil {
		http.Error(w, "bad report body: "+err.Error(), http.StatusBadRequest)
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

// jobView is a pending image job as the station consumes it.
type jobView struct {
	MAC      string `json:"mac"`
	Tag      string `json:"tag"`
	Hardware string `json:"hardware"`
	Status   string `json:"status"`
	// Credential is the device's one-time agent secret, present only in a
	// claim response so the station can bake it into the image. Never stored.
	Credential string `json:"credential,omitempty"`
}

// handleJobs lists a station's pending image jobs (no credentials). The
// station polls this to see what it should image.
func (s *StationAPI) handleJobs(w http.ResponseWriter, r *http.Request) {
	station, ok := s.authStation(w, r)
	if !ok {
		return
	}
	if s.imaging == nil {
		http.Error(w, "imaging execution not configured", http.StatusServiceUnavailable)
		return
	}
	jobs, err := s.imaging.Pending(r.Context(), station)
	if err != nil {
		s.log.Error("list station jobs failed", "station", station, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		out = append(out, jobView{MAC: j.MAC, Tag: j.Tag, Hardware: j.Hardware, Status: string(j.Status)})
	}
	writeJSON(w, http.StatusOK, out)
}

// handleClaim moves a station's pending jobs to imaging and returns them with
// a freshly minted per-device credential, so the installer can write the
// agent secret into the image. The secret is delivered exactly once, over the
// station's authenticated channel - never stored, never shown in the console.
func (s *StationAPI) handleClaim(w http.ResponseWriter, r *http.Request) {
	station, ok := s.authStation(w, r)
	if !ok {
		return
	}
	if s.imaging == nil {
		http.Error(w, "imaging execution not configured", http.StatusServiceUnavailable)
		return
	}
	jobs, err := s.imaging.Pending(r.Context(), station)
	if err != nil {
		s.log.Error("claim station jobs failed", "station", station, "err", err)
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}
	out := make([]jobView, 0, len(jobs))
	for _, j := range jobs {
		v := jobView{MAC: j.MAC, Tag: j.Tag, Hardware: j.Hardware, Status: string(imaging.Imaging)}
		if s.devCreds != nil {
			secret, err := s.devCreds.Issue(r.Context(), j.Tag)
			if err != nil {
				s.log.Error("claim: device credential not issued", "tag", j.Tag, "err", err)
				continue // do not hand out a job the device cannot authenticate for
			}
			v.Credential = secret
		}
		if j.Status == imaging.Pending {
			if err := s.imaging.Report(r.Context(), station, j.MAC, imaging.Imaging, ""); err != nil {
				s.log.Warn("claim: could not mark job imaging", "mac", j.MAC, "err", err)
			}
		}
		out = append(out, v)
	}
	s.log.Info("station claimed jobs", "station", station, "count", len(out))
	writeJSON(w, http.StatusOK, out)
}

// handleJobStatus records a station's progress on one job (imaging done,
// or failed with a reason). The domain rejects an illegal transition.
func (s *StationAPI) handleJobStatus(w http.ResponseWriter, r *http.Request) {
	station, ok := s.authStation(w, r)
	if !ok {
		return
	}
	if s.imaging == nil {
		http.Error(w, "imaging execution not configured", http.StatusServiceUnavailable)
		return
	}
	mac := r.PathValue("mac")
	var in struct {
		Status   string `json:"status,omitempty"`
		Message  string `json:"message,omitempty"`
		Progress *int   `json:"progress,omitempty"`
		Step     string `json:"step,omitempty"`
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "bad status body: "+err.Error(), http.StatusBadRequest)
		return
	}

	// A status transition (guarded) and a progress tick (display-only) arrive on
	// the same endpoint. An empty status is a progress-only tick; a status change
	// resets the step, so apply progress after the transition.
	if in.Status != "" {
		status := imaging.Status(in.Status)
		if !status.Valid() {
			http.Error(w, "unknown status "+in.Status, http.StatusBadRequest)
			return
		}
		msg := in.Message
		// A reported LUKS recovery key is sealed into the secret store and
		// stripped from the message, so plaintext never lands in the job record.
		// The prefix is checked on every status (not just Installed, where the
		// station is expected to send it): a buggy or compromised station must
		// not be able to smuggle plaintext key material to rest by attaching the
		// prefix to, say, a Failed message instead.
		//
		// Sealing is REQUIRED (design 0009, closes threat-model R7): with no
		// secret store configured - or a seal attempt failing - the report is
		// refused with an actionable error instead of keeping the plaintext in
		// the job record for one-shot copy, which the previous behaviour did.
		// The chart ships the secretbox key by default, so any real deploy has
		// the store; a deploy that stripped it must restore it before imaging.
		// The station retries the report, so no key is lost - it is just never
		// at rest unencrypted.
		if key, found := strings.CutPrefix(msg, imaging.LUKSRecoveryPrefix); found {
			if !s.sealLUKS(r.Context(), station, mac, key) {
				http.Error(w, "device-secret store unavailable: refusing to keep a plaintext recovery key; configure the secret store (secretbox key) and re-report", http.StatusServiceUnavailable)
				return
			}
			msg = ""
		}
		if err := s.imaging.Report(r.Context(), station, mac, status, msg); err != nil {
			s.log.Warn("station job status rejected", "station", station, "mac", mac, "status", status, "err", err)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
		s.log.Info("station job status", "station", station, "mac", mac, "status", status)
	}
	if in.Progress != nil || in.Step != "" {
		p := 0
		if in.Progress != nil {
			p = *in.Progress
		}
		if err := s.imaging.ReportProgress(r.Context(), station, mac, p, in.Step); err != nil {
			s.log.Warn("station job progress rejected", "station", station, "mac", mac, "err", err)
			http.Error(w, err.Error(), http.StatusConflict)
			return
		}
	}
	if in.Status == "" && in.Progress == nil && in.Step == "" {
		http.Error(w, "status report needs a status or progress", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// sealLUKS resolves the job's asset tag and seals the reported LUKS recovery
// key into the per-device secret store. It reports whether it sealed the key;
// false makes the caller refuse the report (design 0009 - plaintext recovery
// material never rests in the job record), and the reason is logged loudly so
// the operator sees why imaging is stalled.
func (s *StationAPI) sealLUKS(ctx context.Context, station, mac, key string) bool {
	if s.secrets == nil || !s.secrets.Enabled() {
		s.log.Error("LUKS recovery key reported but no secret store is configured; refusing the report (design 0009)", "station", station, "mac", mac)
		return false
	}
	job, ok, err := s.imaging.Get(ctx, station, mac)
	if err != nil || !ok {
		s.log.Warn("cannot resolve tag to seal LUKS recovery key", "station", station, "mac", mac, "err", err)
		return false
	}
	if err := s.secrets.Store(ctx, job.Tag, secret.LUKS, key, "station:"+station); err != nil {
		s.log.Error("failed to seal LUKS recovery key", "tag", job.Tag, "err", err)
		return false
	}
	s.log.Info("sealed LUKS recovery key", "station", station, "tag", job.Tag)
	return true
}

// authStation resolves and authorizes the station from the path + bearer,
// writing the error response itself. It returns ok=false when the caller
// should stop.
func (s *StationAPI) authStation(w http.ResponseWriter, r *http.Request) (string, bool) {
	if s.stations == nil && s.shared == "" {
		http.Error(w, "station endpoints disabled: no station auth configured", http.StatusForbidden)
		return "", false
	}
	station := r.PathValue("tag")
	if station == "" {
		http.Error(w, "station tag required", http.StatusBadRequest)
		return "", false
	}
	if !s.authorized(r, bearerToken(r), station) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-station"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	return station, true
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
