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

// slugify reduces s to a secret-reference name: lowercase, non-alphanumerics
// become single hyphens, trimmed. Matches the secret name pattern the Secrets
// page enforces ([a-z0-9][a-z0-9-]*).
func slugify(s string) string {
	var b []rune
	prevHyphen := true // avoid a leading hyphen
	for _, r := range s {
		switch {
		case r >= 'A' && r <= 'Z':
			b = append(b, r+('a'-'A'))
			prevHyphen = false
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b = append(b, r)
			prevHyphen = false
		default:
			if !prevHyphen {
				b = append(b, '-')
				prevHyphen = true
			}
		}
	}
	out := string(b)
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	return out
}

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
// unavailable renders "this deployment cannot do that" as a page, with the
// console's own frame around it.
//
// These handlers used to answer with http.NotFound or a line of plain text,
// which is how an operator following a link from the sidebar met a blank
// white page reading "imaging stations need the observed store" - true, and
// useless: no frame, no way back, and a 404 that says the page does not
// exist when the truth is that this deployment has no database behind it.
// The sidebar no longer offers those links, but a bookmark, a notification
// link or a typed URL still arrives here.
func (s *Server) unavailable(w http.ResponseWriter, r *http.Request, v view, msg string) {
	s.render(w, "error", map[string]any{
		"Title": "Unavailable", "Message": msg,
		"Detail":   "",
		"Back":     backLink(r),
		"__status": http.StatusServiceUnavailable,
	}, v)
}

func classifyActionError(err error) (status int, msg, detail string) {
	var verr *ports.ValidationError
	switch {
	case errors.As(err, new(*webForbidden)):
		return http.StatusForbidden, err.Error(), ""
	case errors.As(err, &verr):
		// The gate dumps a multi-line nix trace; show the actionable line and
		// keep the full trace behind a "technical detail" fold.
		return http.StatusUnprocessableEntity, ports.DistillGateError(verr.Detail), verr.Detail
	case errors.Is(err, ports.ErrConflict):
		return http.StatusConflict, "Another change landed first. Reload and try again.", ""
	case errors.Is(err, ports.ErrUnavailable):
		return http.StatusServiceUnavailable, "A dependency is temporarily unavailable. Try again shortly.", ""
	default:
		return http.StatusBadRequest, err.Error(), ""
	}
}
