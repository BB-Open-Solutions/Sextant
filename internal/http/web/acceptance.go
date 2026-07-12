package web

import (
	"net/http"
	"net/url"
	"strings"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// acceptance.go: the risk-acceptance register (ADR 0007, comply-or-explain).
// At a scope an owner may accept a control with a documented justification, so
// a failing-but-accepted control reads as explained, not open. Owner-gated:
// accepting a risk is a governance act, not an everyday edit.

// postAcceptance records a risk acceptance at a scope with a justification.
func (s *Server) postAcceptance(w http.ResponseWriter, r *http.Request, v view) error {
	scope := r.FormValue("scope")
	if err := s.requireWeb(v, scope, identity.Owner); err != nil {
		return err
	}
	key := strings.TrimSpace(r.FormValue("key"))
	reason := strings.TrimSpace(r.FormValue("reason"))
	if err := s.svc.Config.Apply(r.Context(),
		fleet.SetAcceptance(scope, key, reason),
		"acceptance: accept "+key+" at "+scope, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}

// postAcceptanceClear withdraws a risk acceptance at a scope.
func (s *Server) postAcceptanceClear(w http.ResponseWriter, r *http.Request, v view) error {
	scope := r.FormValue("scope")
	if err := s.requireWeb(v, scope, identity.Owner); err != nil {
		return err
	}
	key := strings.TrimSpace(r.FormValue("key"))
	if err := s.svc.Config.Apply(r.Context(),
		fleet.ClearAcceptance(scope, key),
		"acceptance: withdraw "+key+" at "+scope, webAuthor(v)); err != nil {
		return err
	}
	http.Redirect(w, r, "/settings?scope="+url.QueryEscape(scope), http.StatusSeeOther)
	return nil
}
