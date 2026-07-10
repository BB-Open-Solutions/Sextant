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
}

// NewCheckin builds the check-in surface. Both auth sources are optional
// but at least one must be set or check-in is disabled.
func NewCheckin(inv *app.InventoryService, devs DeviceAuthenticator, sharedToken string) *CheckinAPI {
	return &CheckinAPI{inv: inv, devs: devs, shared: sharedToken}
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
	var in checkinBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 320<<10))
	if err := dec.Decode(&in); err != nil {
		http.Error(w, "bad check-in body: "+err.Error(), http.StatusBadRequest)
		return
	}

	secret := bearerToken(r)
	if !c.authorized(r, secret, in.Tag) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-checkin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}

	if err := c.inv.CheckIn(r.Context(), in.CheckIn, in.Facts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
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
