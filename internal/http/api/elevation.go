package api

import (
	"encoding/json"
	"io"
	"net/http"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/app"
)

// elevation.go: the two calls a waiting device makes (#27). It raises a
// request, then polls until the answer comes or the window closes.
//
// Both hang off the check-in API because that is where a device's identity is
// already established. The tag is taken from the authenticated path, never
// from the body: a device that could name another could get somebody else's
// request approved and then claim the answer.
//
// Note what this endpoint does NOT do. It never says "you are authorised" - it
// reports whether an administrator approved, and the device turns that into an
// authentication through PAM, or does not. polkit will not let an agent vouch
// for an identity, and that refusal is what makes the whole feature safe to
// build rather than a way to hand out root over HTTP.

// WithElevation enables the elevation-request endpoints. Without it they are
// absent rather than failing, so a console deployed without the queue does not
// advertise a door that leads nowhere.
func (c *CheckinAPI) WithElevation(svc *app.ElevationService) *CheckinAPI {
	c.elevation = svc
	return c
}

// maxElevationBody bounds the ask. The fields are short by design and an
// operator has to read them.
const maxElevationBody = 4 << 10

type elevationAsk struct {
	User   string `json:"user"`
	Action string `json:"action,omitempty"`
	Reason string `json:"reason,omitempty"`
}

type elevationAnswer struct {
	ID    string `json:"id"`
	State string `json:"state"`
	// Granted is the only field the helper on the device acts on. It is
	// computed rather than derived from State by the caller, so there is one
	// place that decides what an approval is still worth - an approval that
	// arrived after the window has closed is state "approved" and granted
	// false, and a client left to work that out itself would get it wrong.
	Granted bool `json:"granted"`
}

func (c *CheckinAPI) handleElevationRaise(w http.ResponseWriter, r *http.Request) {
	tag, ok := c.deviceFromRequest(w, r)
	if !ok {
		return
	}
	if c.elevation == nil {
		http.Error(w, "elevation requests are not enabled", http.StatusServiceUnavailable)
		return
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxElevationBody))
	if err != nil {
		http.Error(w, "request too large or unreadable", http.StatusRequestEntityTooLarge)
		return
	}
	var ask elevationAsk
	if err := json.Unmarshal(body, &ask); err != nil {
		http.Error(w, "malformed request", http.StatusBadRequest)
		return
	}
	req, err := c.elevation.Raise(r.Context(), tag, ask.User, ask.Action, ask.Reason)
	if err != nil {
		// A bad ask is the caller's fault; anything else is ours. Reporting a
		// missing user as a server error would send somebody hunting through
		// our logs for a mistake made on the device.
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	c.logger().Info("elevation requested", "tag", tag, "user", req.User,
		"id", req.ID, "action", req.Action)
	writeJSON(w, http.StatusCreated, elevationAnswer{ID: req.ID, State: string(req.State)})
}

func (c *CheckinAPI) handleElevationPoll(w http.ResponseWriter, r *http.Request) {
	tag, ok := c.deviceFromRequest(w, r)
	if !ok {
		return
	}
	if c.elevation == nil {
		http.Error(w, "elevation requests are not enabled", http.StatusServiceUnavailable)
		return
	}
	req, found, err := c.elevation.Poll(r.Context(), tag, r.PathValue("id"))
	if err != nil {
		c.logger().Error("failed to read elevation request", "tag", tag, "err", err)
		http.Error(w, "could not read the request", http.StatusInternalServerError)
		return
	}
	if !found {
		// Deliberately the same answer whether the id does not exist or
		// belongs to another device: distinguishing them would let a device
		// probe for other devices' request ids.
		http.Error(w, "no such request", http.StatusNotFound)
		return
	}
	writeJSON(w, http.StatusOK, elevationAnswer{
		ID:      req.ID,
		State:   string(req.State),
		Granted: req.Grants(c.clock()),
	})
}

// deviceFromRequest resolves and authorises the device in the path, answering
// the request itself when it cannot.
func (c *CheckinAPI) deviceFromRequest(w http.ResponseWriter, r *http.Request) (string, bool) {
	tag := r.PathValue("tag")
	bearer := bearerToken(r)
	if bearer == "" || !c.authorized(r, bearer, tag) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="sextant-checkin"`)
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", false
	}
	if c.retired != nil && c.retired(tag) {
		http.Error(w, "device is retired", http.StatusGone)
		return "", false
	}
	return tag, true
}
