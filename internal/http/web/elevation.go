package web

// elevation.go: the operator's side of #27 - the queue of users waiting in
// front of a dialog on their own machine, and the two buttons that answer
// them.
//
// The page is deliberately plain and deliberately small. Somebody is standing
// there for the whole five minutes, so this is not a page to browse; it is a
// page to answer and leave.

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/elevation"
	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/identity"
)

// elevationRow is one waiting request as the operator reads it.
type elevationRow struct {
	ID     string
	Tag    string
	User   string
	Action string
	Reason string
	// Waited is how long this person has been looking at a dialog. The list is
	// ordered by it, because the longest wait is closest to giving up.
	Waited string
	// Left is what remains of the window. An operator who cannot see that a
	// request is about to expire will answer requests that are already dead.
	Left string
	// Expiring marks the last minute, so the page can say so louder than a
	// number in a column.
	Expiring bool
}

func (s *Server) elevationPage(w http.ResponseWriter, r *http.Request, v view) {
	if s.svc.Elevation == nil {
		http.NotFound(w, r)
		return
	}
	// Approving is an act of administration over somebody else's machine, so
	// it needs org-level editor rights - the same bar as any fleet-wide
	// change. A group-scoped operator has no business granting a privileged
	// action on a device they cannot otherwise touch.
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		http.Error(w, err.Error(), http.StatusForbidden)
		return
	}
	pending, err := s.svc.Elevation.Pending(r.Context())
	if err != nil {
		s.log.Warn("elevation queue failed", "err", err)
	}
	now := s.svc.Elevation.Now()
	rows := make([]elevationRow, 0, len(pending))
	for _, req := range pending {
		left := elevation.TTL - now.Sub(req.Created)
		rows = append(rows, elevationRow{
			ID: req.ID, Tag: req.Tag, User: req.User,
			Action: req.Action, Reason: req.Reason,
			Waited:   humanSeconds(req.Waited(now)),
			Left:     humanSeconds(left),
			Expiring: left <= time.Minute,
		})
	}
	s.render(w, "elevation", map[string]any{
		"Title": "Requests", "Nav": "elevation",
		"Rows": rows,
		// The window is shown once, at the top, so an operator who has never
		// seen this page learns why it is empty most of the time.
		"WindowMinutes": int(elevation.TTL / time.Minute),
	}, v)
}

// elevationDecide answers one request. Approve and deny are the same handler
// because they are the same act - a decision, recorded, by a named person -
// and splitting them invites the two paths to drift on what gets logged.
func (s *Server) elevationDecide(w http.ResponseWriter, r *http.Request, v view) error {
	if s.svc.Elevation == nil {
		return fmt.Errorf("elevation requests are not enabled")
	}
	if err := s.requireWeb(v, "org", identity.Editor); err != nil {
		return err
	}
	approve := r.FormValue("decision") == "approve"
	req, err := s.svc.Elevation.Decide(r.Context(), r.PathValue("id"), approve, v.User.Subject)
	if err != nil {
		return err
	}
	// Logged either way. A denial is as much a fact somebody may need to
	// explain later as an approval, and a trail that only records the yeses
	// reads as if nobody ever said no.
	s.log.Info("elevation decided", "id", req.ID, "tag", req.Tag, "user", req.User,
		"approved", approve, "by", v.User.Subject, "action", req.Action)
	http.Redirect(w, r, "/elevation", http.StatusSeeOther)
	return nil
}

// humanSeconds renders a short duration the way somebody watching a clock
// reads it. Never negative: a window that has just closed shows 0s rather
// than counting backwards, which looks like a bug at exactly the moment
// somebody is deciding whether to trust the page.
func humanSeconds(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	if d < time.Minute {
		return strconv.Itoa(int(d.Seconds())) + "s"
	}
	return strconv.Itoa(int(d.Minutes())) + "m " + strconv.Itoa(int(d.Seconds())%60) + "s"
}
