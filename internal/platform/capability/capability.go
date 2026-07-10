// Package capability is the registry the binary grows by (ADR 0006):
// each capability declares its name, whether the current configuration
// enables it, and the routes it contributes. cmd/sextant iterates the
// registry the way Odoo installs modules - navigation, routes and
// permissions come from the module, never from a central god file.
// Compile-time only: no dynamic loading, one reviewed artifact.
package capability

import (
	"log/slog"
	"net/http"
)

// Capability is one functional module of the product.
type Capability interface {
	// Name is the stable slug (metrics, logs, nav).
	Name() string
	// Enabled reports whether the deployment configuration mounts this
	// capability. Disabled capabilities contribute nothing.
	Enabled() bool
	// Routes registers the capability's HTTP surface (API and/or pages).
	Routes(mux *http.ServeMux)
}

// Mount registers every enabled capability and logs the result, returning
// the names that mounted (for the readiness/info endpoints).
func Mount(mux *http.ServeMux, log *slog.Logger, caps ...Capability) []string {
	var mounted []string
	for _, c := range caps {
		if c == nil || !c.Enabled() {
			continue
		}
		c.Routes(mux)
		mounted = append(mounted, c.Name())
		log.Info("capability mounted", "capability", c.Name())
	}
	return mounted
}

// Func adapts plain values into a Capability without a dedicated type.
type Func struct {
	CapName   string
	EnabledFn func() bool
	RoutesFn  func(mux *http.ServeMux)
}

// Name implements Capability.
func (f Func) Name() string { return f.CapName }

// Enabled implements Capability.
func (f Func) Enabled() bool { return f.EnabledFn == nil || f.EnabledFn() }

// Routes implements Capability.
func (f Func) Routes(mux *http.ServeMux) { f.RoutesFn(mux) }
