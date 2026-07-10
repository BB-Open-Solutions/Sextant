package api

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// CheckinAPI serves the device-facing check-in endpoint. It authenticates
// with its own pre-shared token, separate from the operator API token:
// a leaked device credential must never grant config-plane access.
type CheckinAPI struct {
	inv   *app.InventoryService
	token string
}

// NewCheckin builds the check-in surface. An empty token disables it.
func NewCheckin(inv *app.InventoryService, token string) *CheckinAPI {
	return &CheckinAPI{inv: inv, token: token}
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
	if c.token == "" {
		http.Error(w, "check-in disabled: no token configured", http.StatusForbidden)
		return
	}
	if subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(c.token)) != 1 {
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
	if err := c.inv.CheckIn(r.Context(), in.CheckIn, in.Facts); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
