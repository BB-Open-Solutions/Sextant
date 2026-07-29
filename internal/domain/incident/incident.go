// Package incident is the pure compliance engine: given a snapshot of what
// each device is observed to run versus what it is supposed to run, it derives
// the action items an operator must pick up. It is incident-driven - the
// output is a list of problems with a suggested action, not a raw inventory.
// No I/O; the app layer assembles Observations and filters incidents by the
// viewer's scope.
package incident

import (
	"fmt"
	"time"

	"code.overheid.nl/MinBZK/DAWO-Sextant/internal/domain/observed"
)

// Kind classifies an incident so the console can icon and group it.
type Kind string

// The incident kinds the compliance engine emits.
const (
	Behind      Kind = "behind"       // running a different revision than its target
	Offline     Kind = "offline"      // stopped checking in
	NeverSeen   Kind = "never-seen"   // enrolled but never reported
	Errored     Kind = "errored"      // reported a build/apply error
	WipeFailed  Kind = "wipe-failed"  // a crypto-wipe did not complete
	WipeRefused Kind = "wipe-refused" // a device declined a wipe intent
)

// Severity orders incidents; higher is more urgent.
type Severity int

// The severity levels, low to high.
const (
	Info Severity = iota
	Warning
	Critical
)

// Observation is one device's compliance-relevant state, assembled by the app
// layer from the observed plane (deployed revision, last check-in) and the
// config plane (the target revision its group is pinned to).
type Observation struct {
	Tag      string
	Group    string // primary group; "" means org-level
	Deployed string // revision the device reports running ("" = unknown)
	Target   string // revision it should run ("" = follows HEAD, not judged)
	// DeployedRelease/TargetRelease are the human release numbers of those
	// revisions (commit counts; 0 = unknown). With both known the Behind
	// text can say "release 142, target 145" instead of two opaque shas.
	DeployedRelease int
	TargetRelease   int
	Online          bool      // checked in within the online window
	LastSeen        time.Time // zero = never
	Error           string    // device-reported error ("" = none)
	Ack             string    // last remote-action outcome
}

// Incident is one action item for one device.
type Incident struct {
	Kind     Kind
	Severity Severity
	Tag      string
	Group    string
	Scope    string // visibility key: "group:<g>" or "org"
	Title    string
	Detail   string
	Action   string
	Since    time.Time
}

// wipe ack outcomes, mirrored from the observed domain to keep this package
// dependency-free.
const (
	ackWipeFailed  = "wipe-failed"
	ackWipeRefused = "wipe-refused"
)

// Detect turns observations into incidents. One device can raise several (an
// offline device that is also behind). Retired devices must be filtered out by
// the caller; every observation here is expected to be a live device.
func Detect(obs []Observation, now time.Time) []Incident {
	var out []Incident
	for _, o := range obs {
		scope := "org"
		if o.Group != "" {
			scope = "group:" + o.Group
		}
		add := func(k Kind, sev Severity, title, detail, action string, since time.Time) {
			out = append(out, Incident{Kind: k, Severity: sev, Tag: o.Tag, Group: o.Group,
				Scope: scope, Title: title, Detail: detail, Action: action, Since: since})
		}

		switch {
		case o.LastSeen.IsZero() && o.Deployed == "":
			add(NeverSeen, Warning, o.Tag+" has never checked in",
				"Enrolled but no report received yet.",
				"Verify it was imaged and can reach the console.", time.Time{})
		case !o.Online && now.Sub(o.LastSeen) > observed.InactiveWindow:
			// Plain offline is NOT an incident: laptops sleep, travel and
			// take vacations (operator decision 2026-07-29; Intune/FleetDM
			// treat offline as a neutral state). Only prolonged absence
			// escalates - past the window the machine may be lost, broken
			// or shelved, and that IS an action item.
			add(Offline, Warning, o.Tag+" has been offline for over two weeks",
				fmt.Sprintf("Last seen %s.", o.LastSeen.Format("2006-01-02 15:04")),
				"Locate the machine; if it is retired in practice, retire it in the fleet too.", o.LastSeen)
		}

		// An online device on the wrong revision is drifting from its target.
		// Ahead gets its own title and advice: it means an out-of-band change
		// (or a stale pin), not a lagging update - a headline saying "behind"
		// above an AHEAD detail read as a contradiction (operator feedback,
		// 2026-07-28).
		if o.Online && o.Target != "" && o.Deployed != "" && o.Deployed != o.Target {
			detail := fmt.Sprintf("Running %s, target is %s.", short(o.Deployed), short(o.Target))
			title, advice := o.Tag+" is behind",
				"The update has not landed; check the rollout and the device logs."
			if o.DeployedRelease > 0 && o.TargetRelease > 0 {
				diff := o.TargetRelease - o.DeployedRelease
				switch {
				case diff > 0:
					detail = fmt.Sprintf("On release %d, target is release %d (%d behind).",
						o.DeployedRelease, o.TargetRelease, diff)
				case diff < 0:
					title = o.Tag + " is ahead of its target"
					detail = fmt.Sprintf("On release %d, ahead of its release-%d target.",
						o.DeployedRelease, o.TargetRelease)
					advice = "The device runs something the rollout did not stage: an out-of-band change, or a stale pin. Check the pin."
				}
			}
			add(Behind, Warning, title, detail, advice, time.Time{})
		}

		if o.Error != "" {
			add(Errored, Critical, o.Tag+" reported an error",
				o.Error, "Open the device and inspect the failure.", time.Time{})
		}

		switch o.Ack {
		case ackWipeFailed:
			add(WipeFailed, Critical, o.Tag+" wipe did not complete",
				"The device attempted a crypto-wipe but did not confirm completion.",
				"Verify the disk key is destroyed before reusing the device.", time.Time{})
		case ackWipeRefused:
			add(WipeRefused, Warning, o.Tag+" refused the wipe",
				"The device declined the wipe intent (unarmed or an interlock blocked it).",
				"Re-arm the device or clear the interlock, then retry.", time.Time{})
		}
	}
	sortBySeverity(out)
	return out
}

// short trims a revision to a readable prefix.
func short(rev string) string {
	if len(rev) > 12 {
		return rev[:12]
	}
	return rev
}

// sortBySeverity orders incidents most-urgent first, then by kind and tag, so
// the list is deterministic.
func sortBySeverity(in []Incident) {
	for i := 1; i < len(in); i++ {
		for j := i; j > 0 && less(in[j], in[j-1]); j-- {
			in[j], in[j-1] = in[j-1], in[j]
		}
	}
}

func less(a, b Incident) bool {
	if a.Severity != b.Severity {
		return a.Severity > b.Severity
	}
	if a.Kind != b.Kind {
		return a.Kind < b.Kind
	}
	return a.Tag < b.Tag
}
