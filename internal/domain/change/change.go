// Package change models change requests: a named unit of configuration work
// on a git branch, moving through an explicit status flow. Pure domain: the
// state machine lives here; git and persistence live behind ports.
package change

import (
	"fmt"
	"regexp"
	"time"
)

// Status is a change request's position in the flow.
type Status string

const (
	// Draft is being edited.
	Draft Status = "draft"
	// Building means the image build gate is running.
	Building Status = "building"
	// Failed means the build or test gate rejected the change.
	Failed Status = "failed"
	// Ready is built green and approved for merge.
	Ready Status = "ready"
	// Merged is done: the branch landed on main.
	Merged Status = "merged"
	// Abandoned is closed without merging.
	Abandoned Status = "abandoned"
)

// Valid reports whether s is a known status.
func (s Status) Valid() bool {
	switch s {
	case Draft, Building, Failed, Ready, Merged, Abandoned:
		return true
	}
	return false
}

// transitions is the allowed state machine. Merged and Abandoned are
// terminal. Merged is reachable only through Merge (enforced by the
// service): a plain status update must never fake a merge.
var transitions = map[Status][]Status{
	Draft:    {Building, Abandoned},
	Building: {Ready, Failed},
	Failed:   {Draft, Building, Abandoned},
	Ready:    {Merged, Draft, Abandoned},
}

// CanTransition reports whether from -> to is a legal step.
func CanTransition(from, to Status) bool {
	for _, t := range transitions[from] {
		if t == to {
			return true
		}
	}
	return false
}

// CR is one change request. Branch is derived from the ID (cr/<id>).
type CR struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	// Author is the display name; AuthorSubject the stable principal id
	// used for four-eyes enforcement (ADR 0007).
	Author        string    `json:"author"`
	AuthorSubject string    `json:"authorSubject,omitempty"`
	Branch        string    `json:"branch"`
	Status        Status    `json:"status"`
	Error         string    `json:"error,omitempty"` // gate/build rejection detail
	Created       time.Time `json:"created"`
	Updated       time.Time `json:"updated"`

	// Hosts is the union of every edit's blast radius, so Submit can gate the
	// branch against exactly the devices the change can affect. WholeFleet is
	// set once any edit had an unbounded radius (org-wide); from then on the
	// gate validates everything regardless of Hosts.
	Hosts      []string `json:"hosts,omitempty"`
	WholeFleet bool     `json:"wholeFleet,omitempty"`
}

// RecordHosts widens the CR's blast radius with one edit's affected hosts.
// An empty list means the edit was unbounded (org-wide): the whole fleet
// must be validated at submit, whatever earlier edits touched.
func (c *CR) RecordHosts(hosts []string) {
	if len(hosts) == 0 {
		c.WholeFleet = true
		return
	}
	seen := make(map[string]bool, len(c.Hosts))
	for _, h := range c.Hosts {
		seen[h] = true
	}
	for _, h := range hosts {
		if !seen[h] {
			c.Hosts = append(c.Hosts, h)
			seen[h] = true
		}
	}
}

// GateHosts is the host scope Submit validates: nil (everything) when any
// edit was unbounded or the radius is unknown, else the recorded union.
func (c CR) GateHosts() []string {
	if c.WholeFleet || len(c.Hosts) == 0 {
		return nil
	}
	return c.Hosts
}

// Open reports whether the CR is still in progress.
func (c CR) Open() bool { return c.Status != Merged && c.Status != Abandoned }

// idRe constrains a change-request id: it becomes a git ref (cr/<id>) and a
// filesystem path, so it must never contain traversal or ref
// metacharacters. Ported from the proven PoC guard.
var idRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)

// ValidID rejects unsafe change-request ids.
func ValidID(id string) error {
	if !idRe.MatchString(id) {
		return fmt.Errorf("invalid change-request id %q (lowercase slug required)", id)
	}
	return nil
}

// BranchFor is the branch a change lives on. One place, because the gate names
// this ref when it validates (ADR 0020) and a second spelling of it would be a
// bug nobody notices - a validation would quietly fall back to fleet.json-only
// and still say yes.
func BranchFor(id string) string { return "cr/" + id }

// New builds a draft CR. The caller supplies the clock so the domain stays
// deterministic.
func New(id, title, author, authorSubject string, now time.Time) (CR, error) {
	if err := ValidID(id); err != nil {
		return CR{}, err
	}
	if title == "" {
		return CR{}, fmt.Errorf("change request needs a title")
	}
	return CR{
		ID: id, Title: title, Author: author, AuthorSubject: authorSubject,
		Branch: BranchFor(id), Status: Draft,
		Created: now, Updated: now,
	}, nil
}

// Transition moves the CR to a new status after validating the step.
func (c *CR) Transition(to Status, now time.Time) error {
	if !to.Valid() {
		return fmt.Errorf("unknown status %q", to)
	}
	if !CanTransition(c.Status, to) {
		return fmt.Errorf("cannot move change %q from %s to %s", c.ID, c.Status, to)
	}
	c.Status = to
	c.Updated = now
	return nil
}
