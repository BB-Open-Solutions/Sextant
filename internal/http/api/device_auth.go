package api

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
)

// device_auth.go: one endpoint the GATE RUNNER calls to ask "is this a valid
// device credential, and whose".
//
// It exists so the binary cache can be closed without inventing a second
// secret. Every device already holds a per-device credential, written once at
// provisioning and never in git or the store, so it is the natural thing for a
// device to present when it substitutes - and unlike a shared cache token it
// is revocable per machine: retiring a device takes its cache access with it.
//
// The alternative was an agenix secret carrying a shared token, which needs a
// working recipient set on every device before it can be required. That is a
// distribution problem this endpoint does not have.
//
// NOT a public endpoint. It answers only to the gate's own bearer token - the
// same GATE_TOKEN the console already shares with it - because anything that
// turns a credential into a tag is an oracle: without the guard, somebody
// could test candidate credentials against it at their leisure.

// DeviceAuthAPI answers credential-verification questions for the gate.
type DeviceAuthAPI struct {
	devs  *app.DeviceCredentials
	token string
}

// NewDeviceAuth wires the endpoint. An empty token leaves it MOUNTED BUT
// CLOSED rather than absent: a deployment that forgot the token should refuse
// every call, not quietly answer them.
func NewDeviceAuth(devs *app.DeviceCredentials, gateToken string) *DeviceAuthAPI {
	return &DeviceAuthAPI{devs: devs, token: gateToken}
}

// Routes mounts the endpoint.
func (a *DeviceAuthAPI) Routes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/v1/device-auth", a.handle)
}

type deviceAuthRequest struct {
	Credential string `json:"credential"`
}

type deviceAuthResponse struct {
	Tag string `json:"tag"`
}

func (a *DeviceAuthAPI) handle(w http.ResponseWriter, r *http.Request) {
	// Closed when no gate token is configured. An oracle that answers because
	// somebody forgot a secret is worse than one that never worked: the
	// forgetting is silent and the answering is useful.
	if a.token == "" || a.devs == nil ||
		subtle.ConstantTimeCompare([]byte(bearerToken(r)), []byte(a.token)) != 1 {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<10))
	if err != nil {
		http.Error(w, "request too large", http.StatusRequestEntityTooLarge)
		return
	}
	var req deviceAuthRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.Credential) == "" {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	tag, ok := a.devs.Authenticate(r.Context(), req.Credential)
	if !ok {
		// 403 rather than 401: the CALLER authenticated fine, the credential it
		// asked about did not. Answering 401 would send the gate looking at its
		// own token instead of at the device's.
		http.Error(w, "not a valid device credential", http.StatusForbidden)
		return
	}
	writeJSON(w, http.StatusOK, deviceAuthResponse{Tag: tag})
}
