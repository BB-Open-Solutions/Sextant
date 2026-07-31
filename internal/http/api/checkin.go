package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
	domsecret "code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

// DeviceAuthenticator verifies a per-device credential against a claimed
// tag (ADR 0008). Implemented by app.DeviceCredentials.
type DeviceAuthenticator interface {
	AuthenticateTag(ctx context.Context, secret, claimedTag string) bool
}

// CheckinAPI serves the device-facing check-in endpoint. Two auth modes,
// preferring the strong one: a per-device credential (the device proves it
// is the tag it reports), or the shared bridge token (migration only, any
// device can report any tag - kept until every device is re-issued).
type CheckinAPI struct {
	inv    *app.InventoryService
	devs   DeviceAuthenticator
	shared string // shared bridge token; "" disables it
	// retired reports whether a tag is parked. A retired device's reports
	// are refused even with a valid credential: lifecycle beats auth, and
	// the bridge token must not resurrect a parked tag either. nil = no
	// lifecycle source (tests).
	retired func(tag string) bool
	// intent returns a device's pending remote action ("" when none). The
	// check-in response carries it back synchronously, so the device acts
	// on a fresh instruction - no store-and-forward, no replay window.
	intent func(ctx context.Context, tag string) string
	// provision advances the device's image job (the provisioning wizard)
	// from this check-in's posture and ack. Best-effort: it runs after the
	// report is stored and never fails the check-in. nil = no imaging plane.
	provision func(ctx context.Context, c observed.CheckIn) error
	// intentKey signs the wipe replay nonce (design 0004). Empty disables the
	// guard (the by-construction property - intent is the direct response -
	// still holds); set, it signs the wipe response and verifies the wipe ack.
	intentKey []byte
	// secrets escrows a device-reported LUKS recovery key (design 0009).
	// nil/disabled = the key is NOT acknowledged, so the device keeps its
	// copy and retries - recovery material is never silently dropped.
	secrets *app.DeviceSecretsService
	// diag stores sealed diagnostics bundles (design 0010); nil = 503.
	diag *app.DiagnosticsService
	// now is the clock, injectable for tests. nil defaults to time.Now.
	now func() time.Time
	// elevation is the request queue (#27); nil leaves the endpoints
	// answering 503 rather than absent, so a device that asks learns the
	// console cannot help rather than that the path is wrong.
	elevation *app.ElevationService
	// log is optional; logger() falls back to slog.Default().
	log *slog.Logger
}

// logger returns the wired logger or the process default.
func (c *CheckinAPI) logger() *slog.Logger {
	if c.log != nil {
		return c.log
	}
	return slog.Default()
}

// WithLog wires a logger (the capability passes the process logger; tests
// and older call sites fall back to slog.Default).
func (c *CheckinAPI) WithLog(log *slog.Logger) *CheckinAPI {
	c.log = log
	return c
}

// NewCheckin builds the check-in surface. Both auth sources are optional
// but at least one must be set or check-in is disabled.
func NewCheckin(inv *app.InventoryService, devs DeviceAuthenticator, sharedToken string) *CheckinAPI {
	return &CheckinAPI{inv: inv, devs: devs, shared: sharedToken}
}

// WithLifecycle wires the retired-tag predicate (from the config snapshot).
func (c *CheckinAPI) WithLifecycle(retired func(tag string) bool) *CheckinAPI {
	c.retired = retired
	return c
}

// WithIntent wires the pending-intent lookup (from the config snapshot,
// falling back to the provisioning wizard's derived intent).
func (c *CheckinAPI) WithIntent(intent func(ctx context.Context, tag string) string) *CheckinAPI {
	c.intent = intent
	return c
}

// WithIntentKey enables the wipe replay guard (design 0004): the wipe intent
// is signed with this key and the wipe ack is verified against it. Empty
// leaves the guard off.
func (c *CheckinAPI) WithIntentKey(key []byte) *CheckinAPI {
	c.intentKey = key
	return c
}

// WithDeviceSecrets wires the escrow store for provisioning-minted LUKS
// recovery keys (design 0009).
func (c *CheckinAPI) WithDeviceSecrets(secrets *app.DeviceSecretsService) *CheckinAPI {
	c.secrets = secrets
	return c
}

// WithDiagnostics wires the sealed bundle store for the diagnostics upload
// (design 0010). nil (or the deployment kill switch) leaves the endpoint
// answering 503, so a device retries rather than dropping its bundle.
func (c *CheckinAPI) WithDiagnostics(diag *app.DiagnosticsService) *CheckinAPI {
	c.diag = diag
	return c
}

// handleDiagnostics accepts a device's bounded diagnostics bundle (design
// 0010) over the same per-device credential the check-in uses. At most one
// bundle per device; a re-request overwrites. The service seals it before
// storage (journals can contain personal data) and the console enforces
// retention on every read.
func (c *CheckinAPI) handleDiagnostics(w http.ResponseWriter, r *http.Request) {
	// Same authorisation as every other per-device call, through one helper
	// so the two cannot drift into disagreeing about what a retired device
	// may do.
	tag, ok := c.deviceFromRequest(w, r)
	if !ok {
		return
	}
	if c.diag == nil || !c.diag.Enabled() {
		http.Error(w, "diagnostics store unavailable; retry later", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, app.MaxDiagnosticsBundle))
	if err != nil {
		http.Error(w, "bundle too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	if len(body) == 0 {
		http.Error(w, "empty bundle", http.StatusBadRequest)
		return
	}
	if err := c.diag.Put(r.Context(), tag, body); err != nil {
		c.logger().Error("failed to store diagnostics bundle", "tag", tag, "err", err)
		http.Error(w, "could not store bundle", http.StatusInternalServerError)
		return
	}
	c.logger().Info("diagnostics bundle stored", "tag", tag, "bytes", len(body))
	w.WriteHeader(http.StatusNoContent)
}

// WithClock injects the clock (tests).
func (c *CheckinAPI) WithClock(now func() time.Time) *CheckinAPI {
	c.now = now
	return c
}

func (c *CheckinAPI) clock() time.Time {
	if c.now != nil {
		return c.now()
	}
	return time.Now()
}

// WithProvision wires the provisioning-wizard advancement hook.
func (c *CheckinAPI) WithProvision(provision func(ctx context.Context, c observed.CheckIn) error) *CheckinAPI {
	c.provision = provision
	return c
}

// Routes registers the device-facing endpoints.
func (c *CheckinAPI) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/checkin", c.handleCheckin)
	mux.HandleFunc("POST /api/device/{tag}/diagnostics", c.handleDiagnostics)
	mux.HandleFunc("POST /api/device/{tag}/elevation", c.handleElevationRaise)
	mux.HandleFunc("GET /api/device/{tag}/elevation/{id}", c.handleElevationPoll)
}

// checkinBody is the device report: a check-in plus an optional raw
// nixos-facter document.
type checkinBody struct {
	observed.CheckIn
	Facts json.RawMessage `json:"facts,omitempty"`
	// AckNonce/AckTs echo the wipe replay nonce (design 0004) the device
	// received with a wipe intent, so the server can verify a wipe ack is a
	// response to an instruction it recently issued. Empty on ordinary beats
	// and on non-wipe acks.
	AckNonce string `json:"ackNonce,omitempty"`
	AckTs    int64  `json:"ackTs,omitempty"`
	// RecoveryKey is a one-shot LUKS recovery key minted during the
	// provisioning ceremony (design 0009). The server seals it into the
	// device-secret store and confirms with the X-Recovery-Key-Stored
	// response header; only then does the device delete its copy.
	RecoveryKey string `json:"recoveryKey,omitempty"`
}

// isWipeAck reports whether an ack reports a wipe outcome (executed, refused
// or failed) - the acks the replay guard verifies.
func isWipeAck(ack string) bool {
	switch ack {
	case observed.AckWipe, observed.AckWipeRefused, observed.AckWipeFailed:
		return true
	}
	return false
}

func (c *CheckinAPI) handleCheckin(w http.ResponseWriter, r *http.Request) {
	if c.devs == nil && c.shared == "" {
		http.Error(w, "check-in disabled: no device auth configured", http.StatusForbidden)
		return
	}
	// A request with no bearer at all cannot satisfy either auth mode
	// (neither a per-device credential nor the shared token can match an
	// empty secret), so refuse it before reading/decoding the body - the
	// common anonymous-scanner case should not cost a JSON parse of a
	// max-size payload. A request that DOES carry a bearer still needs the
	// body decoded to learn the claimed tag before a per-device credential
	// (bound to that tag) can be checked: unlike the station report, whose
	// tag is a path parameter, this endpoint has no way to learn the tag
	// except from the body.
	secret := bearerToken(r)
	if secret == "" {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-checkin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	var in checkinBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 320<<10))
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "bad check-in body: "+err.Error(), http.StatusBadRequest)
		return
	}

	if !c.authorized(r, secret, in.Tag) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-checkin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	// 410 tells a lingering agent this tag is permanently parked.
	if c.retired != nil && c.retired(in.Tag) {
		http.Error(w, "device is retired", http.StatusGone)
		return
	}

	// Replay guard (design 0004): a wipe ack is only trusted when it echoes a
	// nonce this server signed recently. A forged or replayed wipe ack is
	// dropped (the beat is still recorded, minus the unverified outcome) so it
	// cannot masquerade as a real destructive-action result in the audit
	// trail. Only enforced when the guard is keyed; other acks pass through.
	if len(c.intentKey) > 0 && isWipeAck(in.Ack) {
		// The nonce is signed over the "wipe" intent string; refused/failed
		// acks echo that same nonce back.
		if !verifyIntentNonce(c.intentKey, in.Tag, fleetIntentWipe, in.AckTs, c.clock().Unix(), in.AckNonce) {
			in.Ack = "" // drop the unverified wipe outcome
		}
	}

	if err := c.inv.CheckIn(r.Context(), in.CheckIn, in.Facts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// Escrow a provisioning-minted LUKS recovery key (design 0009): seal it
	// into the device-secret store and confirm via response header; the
	// device deletes its copy only on that confirmation, so a missing or
	// failing store means retry-next-beat, never silent loss. The key is
	// never logged. An oversized value cannot be a systemd-cryptenroll
	// recovery phrase and is refused outright.
	if in.RecoveryKey != "" {
		const maxRecoveryKey = 256
		switch {
		case len(in.RecoveryKey) > maxRecoveryKey:
			http.Error(w, "recovery key exceeds the expected size", http.StatusBadRequest)
			return
		case c.secrets == nil || !c.secrets.Enabled():
			c.logger().Warn("device reported a recovery key but no secret store is configured; device keeps it and retries", "tag", in.Tag)
		default:
			if err := c.secrets.Store(r.Context(), in.Tag, domsecret.LUKS, in.RecoveryKey, "device:"+in.Tag); err != nil {
				c.logger().Error("failed to seal device recovery key", "tag", in.Tag, "err", err)
			} else {
				w.Header().Set("X-Recovery-Key-Stored", "1")
				c.logger().Info("sealed device recovery key", "tag", in.Tag)
			}
		}
	}

	// Advance the provisioning wizard from what this beat reported (posture,
	// executor ack). Best-effort by design: the device's report is already
	// stored, so a wizard hiccup must not turn into a device-visible error.
	if c.provision != nil {
		_ = c.provision(r.Context(), in.CheckIn)
	}

	// A pending remote action rides back on the response (design 0004):
	// the device acts on it locally and echoes an ack next beat. Because
	// this is the direct response to THIS request, it cannot be replayed.
	// The destructive wipe additionally carries a signed nonce + timestamp
	// (the replay guard): the device echoes them in its ack so the server
	// can confirm the ack answers an instruction it issued recently.
	if c.intent != nil {
		if action := c.intent(r.Context(), in.Tag); action != "" {
			body := map[string]any{"intent": action}
			if action == fleetIntentWipe && len(c.intentKey) > 0 {
				ts := c.clock().Unix()
				body["nonce"] = signIntentNonce(c.intentKey, in.Tag, fleetIntentWipe, ts)
				body["ts"] = ts
			}
			writeJSON(w, http.StatusOK, body)
			return
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// authorized accepts a per-device credential bound to the reported tag
// first, then the shared bridge token. A device credential for a DIFFERENT
// tag is rejected even though it is otherwise valid.
func (c *CheckinAPI) authorized(r *http.Request, secret, tag string) bool {
	if c.devs != nil && tag != "" && c.devs.AuthenticateTag(r.Context(), secret, tag) {
		return true
	}
	if c.shared != "" &&
		subtle.ConstantTimeCompare([]byte(secret), []byte(c.shared)) == 1 {
		return true
	}
	return false
}
