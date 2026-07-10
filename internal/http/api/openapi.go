package api

import (
	_ "embed"
	"net/http"
)

// openapi.json is the published machine contract for /api/v1: hand-curated,
// versioned with the code, additive-only after the 1.0 freeze. The contract
// test (openapi_test.go) proves spec and router never drift.
//
//go:embed openapi.json
var openAPISpec []byte

// routeManifest records every route Routes registered, as "METHOD /path",
// so the contract test can compare implementation to specification.
func (a *API) routeManifest() []string { return a.manifest }

// specRoutes registers the spec endpoint. The contract is public by design:
// it contains no secrets and clients need it before they authenticate.
func specRoutes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/v1/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(openAPISpec)
	})
}
