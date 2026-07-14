// Package notify is the pure domain for in-app notifications: a recipient, a
// typed event, and whether it has been read. The app service emits them on
// fleet events (an approval is needed, a rollout finished, the gate failed);
// adapters persist and, later, mail them. No I/O here.
package notify

import (
	"fmt"
	"strings"
	"time"
)

// Kind classifies a notification so the UI can icon and group it, and so a
// recipient can reason about what happened without reading the body.
type Kind string

// The notification kinds the app service emits.
const (
	ApprovalNeeded Kind = "approval-needed" // a change is ready and awaits review
	ChangeMerged   Kind = "change-merged"   // a change the user authored merged
	RolloutDone    Kind = "rollout-done"    // a rollout reached the whole fleet
	GateFailed     Kind = "gate-failed"     // a write was refused by the nix gate
	WipeExecuted   Kind = "wipe-executed"   // a device carried out a crypto-wipe
)

// Valid reports whether k is a known kind.
func (k Kind) Valid() bool {
	switch k {
	case ApprovalNeeded, ChangeMerged, RolloutDone, GateFailed, WipeExecuted:
		return true
	}
	return false
}

// Notification is one message for one audience. Recipient addresses a single
// person by IdP subject; Audience addresses a group or role (e.g. every
// approver) - exactly one of the two is set. A reader receives it when their
// subject matches Recipient, or one of their memberships matches Audience.
type Notification struct {
	ID        string    `json:"id"`
	Tenant    string    `json:"tenant"`
	Recipient string    `json:"recipient,omitempty"` // IdP subject
	Audience  string    `json:"audience,omitempty"`  // group or role name
	Kind      Kind      `json:"kind"`
	Title     string    `json:"title"`
	Body      string    `json:"body,omitempty"`
	Link      string    `json:"link,omitempty"` // in-app path this points at
	Read      bool      `json:"read"`
	CreatedAt time.Time `json:"createdAt"`
}

// Validate rejects a notification that could not be delivered or displayed.
func (n Notification) Validate() error {
	if n.ID == "" {
		return fmt.Errorf("notification: empty id")
	}
	if !n.Kind.Valid() {
		return fmt.Errorf("notification: unknown kind %q", n.Kind)
	}
	if strings.TrimSpace(n.Title) == "" {
		return fmt.Errorf("notification: empty title")
	}
	if (n.Recipient == "") == (n.Audience == "") {
		return fmt.Errorf("notification: set exactly one of recipient or audience")
	}
	return nil
}

// ForReader reports whether a reader with the given subject and memberships
// (groups and role names they hold) should receive this notification.
func (n Notification) ForReader(subject string, memberships []string) bool {
	if n.Recipient != "" {
		return n.Recipient == subject
	}
	for _, m := range memberships {
		if m == n.Audience {
			return true
		}
	}
	return false
}
