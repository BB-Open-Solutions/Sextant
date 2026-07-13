package web

import (
	"net/http"
	"strings"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/notify"
)

// notifications.go: the in-app inbox. Every authenticated user sees the
// notifications addressed to them or to a group they are in, and can mark them
// read. The header bell shows the unread count (computed in authed). Emitting
// happens in the app services; this surface only reads and marks.

// notifRow is one notification prepared for the template: the domain fields
// plus a Material icon and a tone class chosen from the kind.
type notifRow struct {
	ID    string
	Icon  string
	Tone  string // tailwind text-colour class for the icon
	Title string
	Body  string
	Link  string
	Read  bool
	When  time.Time
}

// notifPresent maps a notification kind to its icon and tone. An unknown kind
// still renders with a neutral bell rather than disappearing.
func notifPresent(n notify.Notification) notifRow {
	icon, tone := "notifications", "text-text-secondary"
	switch n.Kind {
	case notify.ApprovalNeeded:
		icon, tone = "rate_review", "text-mint-deep"
	case notify.ChangeMerged:
		icon, tone = "merge", "text-status-success"
	case notify.RolloutDone:
		icon, tone = "rocket_launch", "text-mint-deep"
	case notify.GateFailed:
		icon, tone = "gpp_bad", "text-status-error"
	case notify.WipeExecuted:
		icon, tone = "delete_forever", "text-status-error"
	}
	return notifRow{ID: n.ID, Icon: icon, Tone: tone, Title: n.Title,
		Body: n.Body, Link: n.Link, Read: n.Read, When: n.CreatedAt}
}

// notificationsPage lists the user's newest notifications, unread first-class
// (styled by their read flag). No scope gate: a user always sees their own.
func (s *Server) notificationsPage(w http.ResponseWriter, r *http.Request, v view) {
	data := map[string]any{"Title": "Notifications", "Nav": "notifications"}
	if s.svc.Notify == nil {
		// Notifications need durable storage; without Postgres the page still
		// renders, explaining why it is empty rather than 404-ing.
		data["Unavailable"] = true
		s.render(w, "notifications", data, v)
		return
	}
	items, err := s.svc.Notify.List(r.Context(), v.User.Subject, v.User.Groups, 100)
	if err != nil {
		s.log.Warn("notifications list failed", "err", err)
		data["Error"] = "Could not load notifications."
	}
	rows := make([]notifRow, 0, len(items))
	for _, n := range items {
		rows = append(rows, notifPresent(n))
	}
	data["Items"] = rows
	s.render(w, "notifications", data, v)
}

// postNotificationRead marks one notification read for this user, then returns
// to a safe local path (the notification's link when given, else the inbox).
func (s *Server) postNotificationRead(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Notify != nil {
		if err := s.svc.Notify.MarkRead(r.Context(), r.PathValue("id"), v.User.Subject); err != nil {
			return err
		}
	}
	http.Redirect(w, r, safeLocalPath(r.FormValue("to")), http.StatusSeeOther)
	return nil
}

// postNotificationsReadAll clears the whole inbox's unread state for this user.
func (s *Server) postNotificationsReadAll(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Notify != nil {
		if err := s.svc.Notify.MarkAllRead(r.Context(), v.User.Subject, v.User.Groups); err != nil {
			return err
		}
	}
	http.Redirect(w, r, "/notifications", http.StatusSeeOther)
	return nil
}

// safeLocalPath returns to only if it is an in-app absolute path, guarding the
// redirect against an open-redirect to an external host. Anything else falls
// back to the inbox.
func safeLocalPath(to string) string {
	if strings.HasPrefix(to, "/") && !strings.HasPrefix(to, "//") {
		return to
	}
	return "/notifications"
}
