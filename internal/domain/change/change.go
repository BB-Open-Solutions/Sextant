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
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Author  string    `json:"author"`
	Branch  string    `json:"branch"`
	Status  Status    `json:"status"`
	Error   string    `json:"error,omitempty"` // gate/build rejection detail
	Created time.Time `json:"created"`
	Updated time.Time `json:"updated"`
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

// New builds a draft CR. The caller supplies the clock so the domain stays
// deterministic.
func New(id, title, author string, now time.Time) (CR, error) {
	if err := ValidID(id); err != nil {
		return CR{}, err
	}
	if title == "" {
		return CR{}, fmt.Errorf("change request needs a title")
	}
	return CR{
		ID: id, Title: title, Author: author,
		Branch: "cr/" + id, Status: Draft,
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
