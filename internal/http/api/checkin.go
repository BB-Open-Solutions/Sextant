package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
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
	intent func(tag string) string
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

// WithIntent wires the pending-intent lookup (from the config snapshot).
func (c *CheckinAPI) WithIntent(intent func(tag string) string) *CheckinAPI {
	c.intent = intent
	return c
}

// Routes registers the device-facing endpoint.
func (c *CheckinAPI) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/checkin", c.handleCheckin)
}

// checkinBody is the device report: a check-in plus an optional raw
// nixos-facter document.
type checkinBody struct {
	observed.CheckIn
	Facts json.RawMessage `json:"facts,omitempty"`
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

	if err := c.inv.CheckIn(r.Context(), in.CheckIn, in.Facts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}

	// A pending remote action rides back on the response (design 0004):
	// the device acts on it locally and echoes an ack next beat. Because
	// this is the direct response to THIS request, it cannot be replayed.
	if c.intent != nil {
		if action := c.intent(in.Tag); action != "" {
			writeJSON(w, http.StatusOK, map[string]string{"intent": action})
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
