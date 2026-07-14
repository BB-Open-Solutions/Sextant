package web

import (
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/secret"
)

// secret_ops.go: reveal a per-device secret (LUKS recovery key, break-glass
// admin password). Reveal is org-owner reach and every reveal is recorded
// (who + when) in the store for the audit trail. The plaintext is rendered once
// on the POST response itself - never redirected, so it never lands in a URL,
// the browser history, or a server access log line.

// postSecretReveal reveals one device secret to an owner and shows it once.
func (s *Server) postSecretReveal(w http.ResponseWriter, r *http.Request, v view) error {
	tag := r.PathValue("tag")
	kind := secret.Kind(r.PathValue("kind"))
	if err := s.requireWeb(v, "org", identity.Owner); err != nil {
		return err
	}
	if s.svc.DeviceSecrets == nil || !s.svc.DeviceSecrets.Enabled() {
		return fmt.Errorf("secret store is not configured")
	}
	if err := kind.Validate(); err != nil {
		return err
	}

	by := v.User.Email
	if by == "" {
		by = v.User.Subject
	}
	value, ok, err := s.svc.DeviceSecrets.Reveal(r.Context(), tag, kind, by)
	if err != nil {
		return err
	}
	if !ok {
		http.Error(w, "no such secret", http.StatusNotFound)
		return nil
	}
	s.log.Info("device secret revealed", "tag", tag, "kind", kind, "by", by)
	s.render(w, "secret_reveal", map[string]any{
		"Title": "Secret", "Nav": "enroll",
		"Tag": tag, "Kind": string(kind), "Value": value,
	}, v)
	return nil
}
