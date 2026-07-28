package web

import (
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// org.go: the organisation-settings hub. Sextant runs one deployment per
// organisation (not multi-tenant), so these are the whole org's global
// settings - e-mail, service accounts, the audit log and presentation
// defaults - gathered in one place, distinct from the scoped fleet-config
// editor. A future superadmin surface can steer many such deployments.

// orgPage renders the hub. Org Viewer to see (the audit link needs viewer);
// owner-only surfaces are gated per card.
func (s *Server) orgPage(w http.ResponseWriter, r *http.Request, v view) {
	if err := s.requireWeb(v, "org", identity.Viewer); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	s.render(w, "org", map[string]any{
		"Title": "Organisation", "Nav": "org",
		"Locale":   s.defaultLocale,
		"Timezone": s.defaultTZ,
	}, v)
}
