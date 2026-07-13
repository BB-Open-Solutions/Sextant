package web

import (
	"errors"
	"fmt"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/ports"
)

// errors.go: authorization checks and how a failed action is turned into a
// safe, classified response.

// webForbidden marks an authorization failure so the action wrapper can answer
// 403 (not a generic 400) and show the reason as-is - it carries no internals.
type webForbidden struct{ msg string }

func (e *webForbidden) Error() string { return e.msg }

// requireWeb enforces that the session holds at least role at ref.
func (s *Server) requireWeb(v view, ref string, role identity.Role) error {
	if got := v.roleAt(ref); !got.Meets(role) {
		return &webForbidden{fmt.Sprintf("requires %s at %s (you hold %s)", role, ref, got)}
	}
	return nil
}

// classifyActionError maps a failed action to an HTTP status and a message
// that is safe to show. Authorization failures answer 403 with their own
// (internals-free) reason; a gate rejection carries its actionable detail; a
// lost write-race or an unavailable dependency get a friendly line and the
// right code. Anything else is a handler's own user-facing validation message
// (plain error) - shown as 400 - and is logged in full by the caller either
// way, so nothing sensitive rides on the response.
func classifyActionError(err error) (int, string) {
	var verr *ports.ValidationError
	switch {
	case errors.As(err, new(*webForbidden)):
		return http.StatusForbidden, err.Error()
	case errors.As(err, &verr):
		return http.StatusUnprocessableEntity, verr.Detail
	case errors.Is(err, ports.ErrConflict):
		return http.StatusConflict, "Another change landed first. Reload and try again."
	case errors.Is(err, ports.ErrUnavailable):
		return http.StatusServiceUnavailable, "A dependency is temporarily unavailable. Try again shortly."
	default:
		return http.StatusBadRequest, err.Error()
	}
}
