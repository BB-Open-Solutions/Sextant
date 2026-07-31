// Package elevation models a user asking an administrator for permission to
// do one privileged thing on their own machine, and the administrator
// answering from the console.
//
// WHY IT EXISTS. polkit's answer to "you may not do this" is a dialog asking
// for an ADMINISTRATOR's password. On a fleet machine that means the local
// admin account, so away from the office it is not a slower path - it is no
// path. The predictable outcome is that the admin password gets shared until
// it stops being a secret. Windows solved this years ago: the request becomes
// something an administrator approves centrally, and it is logged.
//
// WHAT MAKES IT SAFE. The grant does not come from this package and cannot.
// polkit will not let an agent vouch for an identity: the response travels
// through the setuid polkit-agent-helper-1, which runs PAM. So a device asks
// here, waits, and PAM turns the answer into an authentication - or does not.
// Sextant decides; it never asserts.
//
// The device reports which action it wants, and that string is CONTEXT rather
// than proof. PAM is not told the polkit action id, so the device's own
// session supplies it, and a session is not a trustworthy narrator about
// itself. Approve on the strength of WHO is asking and WHERE - both of which
// are established by the device's own authenticated check-in - and read the
// action as what the user says they are trying to do.
package elevation

import (
	"fmt"
	"strings"
	"time"
)

// State is where a request stands. A request is created Pending and leaves
// that state exactly once.
type State string

const (
	Pending  State = "pending"
	Approved State = "approved"
	Denied   State = "denied"
	// Expired is a request nobody answered in time. It is a distinct outcome
	// rather than a flavour of Denied: "we said no" and "nobody was there"
	// call for different conversations, and an operator looking at a list of
	// expired requests is looking at a staffing problem, not a policy one.
	Expired State = "expired"
)

// TTL is how long a request waits for an answer.
//
// Short on purpose. Somebody is standing in front of a dialog for the whole
// window, so a generous timeout is not generosity - it is a user staring at a
// frozen screen. Five minutes is long enough for an administrator who is
// watching, and short enough that a user who gets no answer finds out while
// they still remember what they were doing.
const TTL = 5 * time.Minute

// Request is one ask, from one user, on one device.
type Request struct {
	ID string `json:"id"`
	// Tag is the device, taken from its authenticated check-in identity and
	// never from the request body - otherwise any device could raise a
	// request in another's name.
	Tag  string `json:"tag"`
	User string `json:"user"`
	// Action is what the session says it is trying to do, for the approver to
	// read. Context, not proof - see the package comment.
	Action string `json:"action,omitempty"`
	// Reason is what the user typed, when they were given the chance. The
	// single most useful field on the page and the only one a human wrote.
	Reason    string    `json:"reason,omitempty"`
	State     State     `json:"state"`
	Created   time.Time `json:"created"`
	Decided   time.Time `json:"decided,omitempty"`
	DecidedBy string    `json:"decidedBy,omitempty"`
}

// Valid reports whether a request is well-formed enough to store.
func (r Request) Valid() error {
	switch {
	case strings.TrimSpace(r.ID) == "":
		return fmt.Errorf("elevation request has no id")
	case strings.TrimSpace(r.Tag) == "":
		return fmt.Errorf("elevation request has no device")
	case strings.TrimSpace(r.User) == "":
		return fmt.Errorf("elevation request has no user")
	case r.Created.IsZero():
		return fmt.Errorf("elevation request has no creation time")
	}
	return nil
}

// Resolve is the state a request is actually in at time now.
//
// Expiry is computed rather than stored, so a request cannot sit Pending
// forever because the process that was going to expire it died. A stored state
// that depends on a timer is a state that lies after a restart.
func (r Request) Resolve(now time.Time) State {
	if r.State != Pending {
		return r.State
	}
	if now.Sub(r.Created) >= TTL {
		return Expired
	}
	return Pending
}

// Grants reports whether this request authorises the elevation right now.
//
// The only place that question is answered. Note what it refuses: an approval
// that arrived after the window had already closed does NOT grant. Otherwise
// an administrator could approve a request the user gave up on ten minutes
// ago, and the next privileged action that user attempts would sail through
// without anybody asking - an approval must apply to the moment it was for.
func (r Request) Grants(now time.Time) bool {
	return r.State == Approved && !r.Decided.IsZero() &&
		r.Decided.Sub(r.Created) < TTL && now.Sub(r.Created) < TTL
}

// Decide records an answer. It refuses to answer a request that is no longer
// waiting, so two administrators clicking at once cannot produce two verdicts,
// and an expired request cannot be revived by a late click.
func (r Request) Decide(approve bool, by string, now time.Time) (Request, error) {
	if s := r.Resolve(now); s != Pending {
		return r, fmt.Errorf("this request is already %s", s)
	}
	if strings.TrimSpace(by) == "" {
		return r, fmt.Errorf("an approval must record who gave it")
	}
	r.State, r.Decided, r.DecidedBy = Denied, now, by
	if approve {
		r.State = Approved
	}
	return r, nil
}

// Waited is how long the user has been standing in front of the dialog, for
// the console to show. Ages a decided request from its decision instead of
// continuing to count.
func (r Request) Waited(now time.Time) time.Duration {
	if !r.Decided.IsZero() {
		return r.Decided.Sub(r.Created)
	}
	if d := now.Sub(r.Created); d < TTL {
		return d
	}
	return TTL
}
