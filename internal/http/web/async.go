package web

// async.go: the grace-window runner for gated writes. A nix validation costs
// seconds to tens of seconds (it is a real evaluation, not a lookup), and an
// operator must not sit on a spinner for it: fast validations answer inline,
// slow ones detach to the background and report their outcome as an in-app
// notification. The write itself is unchanged - same gate, same commit, same
// fail-closed semantics - only WHERE the operator waits moves.

import (
	"context"
	"net/http"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/fleet"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// gateGraceWindow is how long a gated write may run before the request stops
// waiting for it. Structural writes finish in milliseconds; a warm scoped
// eval in seconds; anything beyond this is background material.
const gateGraceWindow = 3 * time.Second

// runGated executes fn (a full gated write: validate + commit). If it
// finishes within the grace window its error - or nil - is handled inline
// exactly as before. If it is still running, the request returns immediately
// with a "validating" notification and the outcome (applied / refused with
// the distilled reason) arrives as a notification when the write completes.
//
// desc names the change for those notifications ("group pilot re-parented").
func (s *Server) runGated(r *http.Request, v view, desc string, fn func(ctx context.Context) error) error {
	// The write must survive the request: WithoutCancel keeps the request's
	// values (tracing) but not its cancellation - a browser redirect must
	// not abort a half-validated commit. The gate carries its own timeouts.
	ctx := context.WithoutCancel(r.Context())
	done := make(chan error, 1)
	go func() { done <- fn(ctx) }()

	select {
	case err := <-done:
		return err
	case <-time.After(gateGraceWindow):
	}

	// Detached. Tell the operator NOW (the bell badge shows immediately on
	// the page they land on), then report the outcome when it arrives.
	s.notifyUser(ctx, v, notify.Notification{
		Kind:  notify.WritePending,
		Title: v.L.T("async.pending_title"),
		Body:  desc + " — " + v.L.T("async.pending_body"),
		Link:  "/notifications",
	})
	go func() {
		err := <-done
		if err != nil {
			status, msg, _ := classifyActionError(err)
			_ = status
			s.log.Warn("background write refused", "desc", desc, "err", err)
			s.notifyUser(ctx, v, notify.Notification{
				Kind:  notify.GateFailed,
				Title: v.L.T("async.failed_title"),
				Body:  desc + " — " + msg,
				Link:  "/notifications",
			})
			return
		}
		s.notifyUser(ctx, v, notify.Notification{
			Kind:  notify.WriteApplied,
			Title: v.L.T("async.applied_title"),
			Body:  desc,
		})
	}()
	return nil
}

// applyGated is the one-line form for the common case: a fleet mutation
// through the gated write transaction under the grace window. Argument
// order mirrors ConfigService.Apply so call sites convert mechanically.
func (s *Server) applyGated(r *http.Request, v view, mut fleet.Mutation, msg string, hosts ...string) error {
	author := webAuthor(v)
	return s.runGated(r, v, msg, func(ctx context.Context) error {
		return s.svc.Config.Apply(ctx, mut, msg, author, hosts...)
	})
}

// notifyUser emits a notification to the acting operator. Nil-safe: a
// console without the notification store (no Postgres) simply stays quiet -
// the write itself is unaffected.
func (s *Server) notifyUser(ctx context.Context, v view, n notify.Notification) {
	if s.svc.Notify == nil || v.User.Subject == "" {
		return
	}
	n.Recipient = v.User.Subject
	if err := s.svc.Notify.Emit(ctx, n); err != nil {
		s.log.Warn("notify emit failed", "err", err)
	}
}
